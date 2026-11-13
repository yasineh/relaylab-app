package replay

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/identity"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

// Handler serves the replay API. It must be mounted behind
// identity.RequireSession and the same-origin and JSON checks.
type Handler struct {
	svc                  *Service
	reads                *Reads
	pageDefault, pageMax int
}

// NewHandler returns the replay HTTP handler.
func NewHandler(svc *Service, reads *Reads, pageDefault, pageMax int) *Handler {
	return &Handler{svc: svc, reads: reads, pageDefault: pageDefault, pageMax: pageMax}
}

// EventRoutes registers the routes under /projects/{projectId}/events/{eventId}/replays.
func (h *Handler) EventRoutes(r chi.Router) {
	r.Post("/preview", h.preview)
	r.Post("/", h.create)
	r.Get("/", h.listForEvent)
}

// ProjectRoutes registers /projects/{projectId}/replays.
func (h *Handler) ProjectRoutes(r chi.Router) {
	r.Get("/", h.list)
	r.Get("/{replayId}", h.get)
}

// AuditRoutes registers /projects/{projectId}/audit-log (read-only).
func (h *Handler) AuditRoutes(r chi.Router) {
	r.Get("/", h.auditLog)
}

// parseID parses a path id; anything else is a 404, like missing resources.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	return id, err == nil && id != uuid.Nil
}

// scope returns the session user and the project id (and event id when
// withEvent). The owner comes only from the validated session.
func scope(w http.ResponseWriter, r *http.Request, withEvent bool) (string, uuid.UUID, uuid.UUID, bool) {
	id, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
		return "", uuid.Nil, uuid.Nil, false
	}
	project, ok := parseID(chi.URLParam(r, "projectId"))
	if !ok {
		httpx.NotFound(w, r)
		return "", uuid.Nil, uuid.Nil, false
	}
	var event uuid.UUID
	if withEvent {
		if event, ok = parseID(chi.URLParam(r, "eventId")); !ok {
			httpx.NotFound(w, r)
			return "", uuid.Nil, uuid.Nil, false
		}
	}
	return id.UserID, project, event, true
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	owner, project, event, ok := scope(w, r, true)
	if !ok {
		return
	}
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p, err := h.svc.Preview(r.Context(), owner, project, event, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	owner, project, event, ok := scope(w, r, true)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > MaxIdempotencyKey || strings.ContainsAny(key, "\r\n\x00") {
		httpx.WriteValidationError(w, r, map[string]string{"Idempotency-Key": "Must be at most 128 characters."})
		return
	}
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id, created, err := h.svc.Create(r.Context(), owner, project, event, in, key)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.reads.Get(r.Context(), owner, project, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK // the replay an earlier request with this key created
	}
	logging.FromContext(r.Context()).Info("replay created",
		slog.String("replay_id", id.String()), slog.String("project_id", project.String()),
		slog.String("destination_type", d.Destination.Type), slog.String("destination_host", d.host()),
		slog.Bool("idempotent_repeat", !created))
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, d)
}

// fail maps service errors to the error envelope.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var fe *FieldError
	var rl *RateLimitedError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.As(err, &fe):
		if fe.Code == "validation_failed" {
			httpx.WriteValidationError(w, r, fe.Fields)
			return
		}
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, httpx.ErrorBody{Error: httpx.ErrorDetail{
			Code: fe.Code, Message: "This destination cannot be used.", RequestID: httpx.RequestID(r.Context()), Fields: fe.Fields,
		}})
	case errors.Is(err, ErrUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "replay_unavailable",
			"Replay is unavailable right now. It is turned off, or the outbound safety check has not reported in; sending to a test sink may still work.")
	case errors.Is(err, ErrQueueFull):
		httpx.WriteError(w, r, http.StatusTooManyRequests, "replay_queue_full",
			"This project already has the maximum number of replays waiting or running. Try again when some finish.")
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(rl.RetryAfter.Seconds()))))
		httpx.WriteError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many replays. Wait a moment and try again.")
	case errors.Is(err, ErrIdempotencyConflict):
		httpx.WriteError(w, r, http.StatusConflict, "idempotency_conflict",
			"This Idempotency-Key was already used for a different replay request.")
	case errors.Is(err, errInvalidCursor):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_cursor", "The page cursor is invalid. Start again from the first page.")
	default:
		logging.FromContext(r.Context()).Error("replay request failed", slog.String("error", err.Error()))
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
	}
}
