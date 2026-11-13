package events

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/identity"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

// Handler serves /api/v1/projects/{projectId}/events. It must be mounted
// behind identity.RequireSession and the same-origin and JSON checks.
type Handler struct {
	svc                  *Service
	pageDefault, pageMax int
	streamOpts           *StreamOptions // nil: no live stream route
}

// NewHandler returns the HTTP handler for events, with page sizes from the
// central limits (LIMIT_PAGE_DEFAULT, LIMIT_PAGE_MAX).
func NewHandler(svc *Service, pageDefault, pageMax int) *Handler {
	return &Handler{svc: svc, pageDefault: pageDefault, pageMax: pageMax}
}

// Routes registers the event routes (mounted at /projects/{projectId}/events).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.list)
	if h.streamOpts != nil {
		r.Get("/stream", h.stream)
	}
	r.Get("/{eventId}", h.get)
	r.Delete("/{eventId}", h.delete)
	r.Get("/{eventId}/body", h.body)
}

// EndpointRoutes registers the event routes under an endpoint (mounted at
// /projects/{projectId}/endpoints).
func (h *Handler) EndpointRoutes(r chi.Router) {
	r.Delete("/{endpointId}/events", h.clearEndpoint)
}

type summaryJSON struct {
	ID           string    `json:"id"`
	EndpointID   string    `json:"endpoint_id"`
	EndpointName string    `json:"endpoint_name"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	ContentType  *string   `json:"content_type"`
	BodySize     int       `json:"body_size"`
	ReceivedAt   time.Time `json:"received_at"`
}

type entryJSON struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted"`
}

// bodyJSON carries the body as text (utf8) or base64 (binary), never both.
// Text is not HTML-escaped beyond JSON encoding; clients render it as text.
type bodyJSON struct {
	Encoding    string  `json:"encoding"`
	Text        *string `json:"text,omitempty"`
	Base64      *string `json:"base64,omitempty"`
	IsJSON      bool    `json:"is_json"`
	JSONInvalid bool    `json:"json_invalid"`
	Size        int     `json:"size"`
}

type detailJSON struct {
	summaryJSON
	RawQuery       string      `json:"raw_query"`
	Query          []entryJSON `json:"query"`
	Headers        []entryJSON `json:"headers"`
	RedactedCount  int         `json:"redacted_count"`
	Body           bodyJSON    `json:"body"`
	SourceIPPrefix *string     `json:"source_ip_prefix"`
	UserAgent      *string     `json:"user_agent"`
	CorrelationID  string      `json:"correlation_id"`
}

func toSummary(s Summary) summaryJSON {
	return summaryJSON{
		ID: s.ID.String(), EndpointID: s.EndpointID.String(), EndpointName: s.EndpointName,
		Method: s.Method, Path: s.Path, ContentType: s.ContentType, BodySize: s.BodySize, ReceivedAt: s.ReceivedAt,
	}
}

func toDetail(d Detail) detailJSON {
	out := detailJSON{
		summaryJSON: toSummary(d.Summary), RawQuery: d.RawQuery, RedactedCount: d.RedactedCount,
		Query: make([]entryJSON, len(d.Query)), Headers: make([]entryJSON, len(d.Headers)),
		Body:           bodyJSON{Encoding: d.Body.Encoding, IsJSON: d.Body.IsJSON, JSONInvalid: d.Body.JSONInvalid, Size: len(d.Body.Bytes)},
		SourceIPPrefix: d.SourceIPPrefix, UserAgent: d.UserAgent, CorrelationID: d.CorrelationID,
	}
	for i, q := range d.Query {
		out.Query[i] = entryJSON(q)
	}
	for i, hd := range d.Headers {
		out.Headers[i] = entryJSON(hd)
	}
	if d.Body.Encoding == "utf8" {
		t := string(d.Body.Bytes)
		out.Body.Text = &t
	} else {
		b := base64.StdEncoding.EncodeToString(d.Body.Bytes)
		out.Body.Base64 = &b
	}
	return out
}

// scope returns the session user and the parsed path ids. The owner comes
// only from the validated session.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request, withEvent bool) (string, uuid.UUID, uuid.UUID, bool) {
	id, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
		return "", uuid.Nil, uuid.Nil, false
	}
	projectID, err := ParseID(chi.URLParam(r, "projectId"))
	if err != nil {
		h.fail(w, r, err)
		return "", uuid.Nil, uuid.Nil, false
	}
	var eventID uuid.UUID
	if withEvent {
		if eventID, err = ParseID(chi.URLParam(r, "eventId")); err != nil {
			h.fail(w, r, err)
			return "", uuid.Nil, uuid.Nil, false
		}
	}
	return id.UserID, projectID, eventID, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	owner, projectID, _, ok := h.scope(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	f, cursor, limit, err := ParseList(ListParams{
		EndpointID: q["endpoint_id"], Methods: q["method"], ReceivedAfter: q["received_after"],
		ReceivedBefore: q["received_before"], ContentType: q["content_type"], Sort: q["sort"],
		Limit: q["limit"], Cursor: q["cursor"],
	}, h.pageDefault, h.pageMax)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), owner, projectID, f, cursor, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]summaryJSON, len(page.Items))
	for i, s := range page.Items {
		items[i] = toSummary(s)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

// get ignores every query parameter, including a legacy "reveal": original
// credential values are not stored, so there is nothing to reveal.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, projectID, eventID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	d, err := h.svc.Get(r.Context(), owner, projectID, eventID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, toDetail(d))
}

// body returns the exact stored bytes as a download that browsers never
// render: octet-stream, attachment, nosniff, and a sandbox CSP.
func (h *Handler) body(w http.ResponseWriter, r *http.Request) {
	owner, projectID, eventID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	b, err := h.svc.RawBody(r.Context(), owner, projectID, eventID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/octet-stream")
	hd.Set("Content-Disposition", `attachment; filename="event-`+eventID.String()+`.bin"`)
	hd.Set("Content-Length", strconv.Itoa(len(b)))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "sandbox")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(b) //nolint:gosec // G705: inert download (octet-stream, attachment, nosniff, CSP sandbox)
	}
}

// delete permanently deletes one event and its replays: 204, or 404 for
// events the user cannot see.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	owner, projectID, eventID, ok := h.scope(w, r, true)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), owner, projectID, eventID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clearEndpoint deletes an endpoint's events in batches for up to 30
// seconds and reports how many went; clients repeat until deleted is 0.
func (h *Handler) clearEndpoint(w http.ResponseWriter, r *http.Request) {
	owner, projectID, _, ok := h.scope(w, r, false)
	if !ok {
		return
	}
	endpointID, err := ParseID(chi.URLParam(r, "endpointId"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.svc.ClearEndpoint(r.Context(), owner, projectID, endpointID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrInvalidCursor):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_cursor",
			"The page cursor is invalid or belongs to different filters. Start again from the first page.")
	case errors.As(err, &ve):
		httpx.WriteValidationError(w, r, ve.Fields)
	default:
		logging.FromContext(r.Context()).Error("event request failed", slog.String("error", err.Error()))
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
	}
}
