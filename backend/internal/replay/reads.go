package replay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/redact"
)

var errInvalidCursor = errors.New("invalid cursor")

// States and terminal outcomes.
var (
	States = []string{"queued", "running", "succeeded", "failed"}
)

// Reads lists and shows replays and audit entries for the owner.
type Reads struct {
	pool *pgxpool.Pool
	red  *redact.Redactor
}

// NewReads returns the read side of the replay API.
func NewReads(pool *pgxpool.Pool, red *redact.Redactor) *Reads { return &Reads{pool: pool, red: red} }

// ReplayJSON is a replay as the API returns it. Detail-only fields are
// omitted from lists.
type ReplayJSON struct {
	ID              string        `json:"id"`
	EventID         string        `json:"event_id"`
	State           string        `json:"state"`
	Outcome         *string       `json:"outcome"`
	FailureCategory *string       `json:"failure_category"`
	FailureMessage  *string       `json:"failure_message"`
	Destination     Destination   `json:"destination"`
	Target          string        `json:"target"`
	Method          string        `json:"method"`
	Tries           int           `json:"tries"`
	ResponseStatus  *int          `json:"response_status"`
	DurationMS      *int          `json:"duration_ms"`
	CreatedAt       time.Time     `json:"created_at"`
	StartedAt       *time.Time    `json:"started_at,omitempty"`
	FinishedAt      *time.Time    `json:"finished_at"`
	HeaderOverrides []SentHeader  `json:"header_overrides,omitempty"`
	Response        *ResponseJSON `json:"response,omitempty"`
}

// ResponseJSON is the recorded response of the final try.
type ResponseJSON struct {
	Status  int          `json:"status"`
	Headers []SentHeader `json:"headers"`
	Body    BodyJSON     `json:"body"`
}

// BodyJSON is the response excerpt as text (utf8) or base64 (binary).
// Clients render it as text; it is never HTML.
type BodyJSON struct {
	Encoding  string  `json:"encoding"`
	Text      *string `json:"text,omitempty"`
	Base64    *string `json:"base64,omitempty"`
	Size      int     `json:"size"`
	Truncated bool    `json:"truncated"`
}

func (d ReplayJSON) host() string {
	if d.Destination.URL == nil {
		return ""
	}
	return hostOf(*d.Destination.URL)
}

type summaryRow struct {
	id, eventID, sinkID pgtype.UUID
	destType            string
	targetURL           pgtype.Text
	sinkName            pgtype.Text
	method, state       string
	outcome             pgtype.Text
	tries               int32
	status, durationMS  pgtype.Int4
	created, finished   pgtype.Timestamptz
}

func toJSON(s summaryRow) ReplayJSON {
	d := ReplayJSON{
		ID: uuid.UUID(s.id.Bytes).String(), EventID: uuid.UUID(s.eventID.Bytes).String(), State: s.state,
		Method: s.method, Tries: int(s.tries), CreatedAt: s.created.Time.UTC(),
	}
	if s.destType == DestinationURL {
		u := s.targetURL.String
		d.Destination = Destination{Type: DestinationURL, URL: &u}
		d.Target = u
	} else {
		d.Destination = Destination{Type: DestinationSink}
		d.Target = "Test sink (endpoint deleted)"
		if s.sinkID.Valid {
			id := uuid.UUID(s.sinkID.Bytes).String()
			name := s.sinkName.String
			d.Destination.EndpointID, d.Destination.EndpointName = &id, &name
			d.Target = "Test sink: " + name
		}
	}
	if s.outcome.Valid {
		o := s.outcome.String
		d.Outcome = &o
		if o != "succeeded" {
			d.FailureCategory = &o
		}
	}
	if s.status.Valid {
		v := int(s.status.Int32)
		d.ResponseStatus = &v
	}
	if s.durationMS.Valid {
		v := int(s.durationMS.Int32)
		d.DurationMS = &v
	}
	if s.finished.Valid {
		t := s.finished.Time.UTC()
		d.FinishedAt = &t
	}
	return d
}

// Get returns one replay with its detail.
func (rd *Reads) Get(ctx context.Context, owner string, project, id uuid.UUID) (ReplayJSON, error) {
	r, err := store.New(rd.pool).GetReplay(ctx, store.GetReplayParams{ID: pgUUID(id), ProjectID: pgUUID(project), OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReplayJSON{}, ErrNotFound
	}
	if err != nil {
		return ReplayJSON{}, fmt.Errorf("get replay: %w", err)
	}
	d := toJSON(summaryRow{
		id: r.ID, eventID: r.EventID, sinkID: r.SinkEndpointID, destType: r.DestinationType, targetURL: r.TargetUrl,
		sinkName: r.SinkEndpointName, method: r.Method, state: r.State, outcome: r.Outcome, tries: r.Tries,
		status: r.ResponseStatus, durationMS: r.DurationMs, created: r.CreatedAt, finished: r.FinishedAt,
	})
	if r.FailureMessage.Valid {
		m := r.FailureMessage.String
		d.FailureMessage = &m
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time.UTC()
		d.StartedAt = &t
	}
	d.HeaderOverrides = []SentHeader{}
	for _, n := range r.HeaderOverrideNames {
		d.HeaderOverrides = append(d.HeaderOverrides, SentHeader{Name: n, Value: redact.Marker, Redacted: true})
	}
	if r.ResponseStatus.Valid {
		resp := &ResponseJSON{Status: int(r.ResponseStatus.Int32), Headers: []SentHeader{}}
		var hs [][2]string
		if len(r.ResponseHeaders) > 0 {
			if err := json.Unmarshal(r.ResponseHeaders, &hs); err != nil {
				return ReplayJSON{}, fmt.Errorf("decode response headers: %w", err)
			}
		}
		for _, h := range hs {
			resp.Headers = append(resp.Headers, SentHeader{Name: h[0], Value: h[1], Redacted: h[1] == redact.Marker && rd.red.SensitiveHeader(h[0])})
		}
		resp.Body = bodyJSON(r.ResponseBody, r.ResponseTruncated)
		d.Response = resp
	}
	return d, nil
}

func bodyJSON(b []byte, truncated bool) BodyJSON {
	out := BodyJSON{Size: len(b), Truncated: truncated}
	// A truncated excerpt may end inside a multi-byte character; trim it.
	text := b
	if truncated {
		for i := 0; i < utf8.UTFMax && len(text) > 0 && !utf8.Valid(text); i++ {
			text = text[:len(text)-1]
		}
	}
	if utf8.Valid(text) {
		s := string(text)
		out.Encoding, out.Text = "utf8", &s
	} else {
		s := base64.StdEncoding.EncodeToString(b)
		out.Encoding, out.Base64 = "binary", &s
	}
	return out
}

// ListParams are the raw list query parameters.
type ListParams struct {
	EventID, State, Outcome, Limit, Cursor []string
}

// List returns one page of replays, newest first.
func (rd *Reads) List(ctx context.Context, owner string, project uuid.UUID, p ListParams, event *uuid.UUID, pageDefault, pageMax int) ([]ReplayJSON, *string, error) {
	arg := store.ListReplaysParams{ProjectID: pgUUID(project), OwnerID: owner}
	fields := map[string]string{}
	single := func(name string, v []string) (string, bool) {
		switch len(v) {
		case 0:
			return "", false
		case 1:
			return v[0], true
		}
		fields[name] = "Give this parameter once."
		return "", false
	}
	if event != nil {
		arg.EventID = pgUUID(*event)
	} else if v, ok := single("event_id", p.EventID); ok {
		if id, ok := parseID(v); ok {
			arg.EventID = pgUUID(id)
		} else {
			fields["event_id"] = "Must be an event id."
		}
	}
	if v, ok := single("state", p.State); ok {
		if !slices.Contains(States, v) {
			fields["state"] = "Must be queued, running, succeeded, or failed."
		}
		arg.State = pgtype.Text{String: v, Valid: true}
	}
	if v, ok := single("outcome", p.Outcome); ok {
		if v == "" || len(v) > 64 {
			fields["outcome"] = "Must be succeeded or a failure category."
		}
		arg.Outcome = pgtype.Text{String: v, Valid: true}
	}
	limit, err := pageLimit(p.Limit, pageDefault, pageMax, fields, single)
	if len(fields) > 0 || err != nil {
		return nil, nil, &FieldError{Code: "validation_failed", Fields: fields}
	}
	if v, ok := single("cursor", p.Cursor); ok {
		at, id, err := decodeCursor(v)
		if err != nil {
			return nil, nil, errInvalidCursor
		}
		arg.CursorCreatedAt = pgtype.Timestamptz{Time: at, Valid: true}
		arg.CursorID = pgUUID(id)
	}
	arg.RowLimit = int32(limit + 1) //nolint:gosec // limit <= page max
	rows, err := store.New(rd.pool).ListReplays(ctx, arg)
	if err != nil {
		return nil, nil, fmt.Errorf("list replays: %w", err)
	}
	if len(rows) == 0 {
		if err := rd.checkProject(ctx, owner, project); err != nil {
			return nil, nil, err
		}
	}
	out := make([]ReplayJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, toJSON(summaryRow{
			id: r.ID, eventID: r.EventID, sinkID: r.SinkEndpointID, destType: r.DestinationType, targetURL: r.TargetUrl,
			sinkName: r.SinkEndpointName, method: r.Method, state: r.State, outcome: r.Outcome, tries: r.Tries,
			status: r.ResponseStatus, durationMS: r.DurationMs, created: r.CreatedAt, finished: r.FinishedAt,
		}))
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		last := rows[limit-1]
		c := encodeCursor(last.CreatedAt.Time, last.ID.Bytes)
		next = &c
	}
	return out, next, nil
}

func pageLimit(v []string, def, maxN int, fields map[string]string, single func(string, []string) (string, bool)) (int, error) {
	raw, ok := single("limit", v)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxN || strings.TrimSpace(raw) != raw || strings.HasPrefix(raw, "+") {
		fields["limit"] = fmt.Sprintf("Must be a whole number from 1 to %d.", maxN)
		return 0, errors.New("bad limit")
	}
	return n, nil
}

func (rd *Reads) checkProject(ctx context.Context, owner string, project uuid.UUID) error {
	if _, err := store.New(rd.pool).GetProject(ctx, store.GetProjectParams{ID: pgUUID(project), OwnerID: owner}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check project: %w", err)
	}
	return nil
}

// encodeCursor and decodeCursor key pages by (created_at, id). Cursors are
// opaque but not secret: queries stay inside the owner's project.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(int(at.UnixMicro())) + "|" + id.String()))
}

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, errInvalidCursor
	}
	micros, err := strconv.Atoi(ts)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return time.UnixMicro(int64(micros)).UTC(), id, nil
}

// AuditJSON is one audit entry.
type AuditJSON struct {
	ID           string            `json:"id"`
	Action       string            `json:"action"`
	ActorUserID  *string           `json:"actor_user_id"`
	ActorName    *string           `json:"actor_name"`
	ResourceType string            `json:"resource_type"`
	ResourceID   *string           `json:"resource_id"`
	Metadata     map[string]string `json:"metadata"`
	CreatedAt    time.Time         `json:"created_at"`
}

// Audit returns one page of the project's audit log, newest first.
func (rd *Reads) Audit(ctx context.Context, owner string, project uuid.UUID, limitRaw, cursorRaw []string, pageDefault, pageMax int) ([]AuditJSON, *string, error) {
	fields := map[string]string{}
	single := func(name string, v []string) (string, bool) {
		switch len(v) {
		case 0:
			return "", false
		case 1:
			return v[0], true
		}
		fields[name] = "Give this parameter once."
		return "", false
	}
	limit, err := pageLimit(limitRaw, pageDefault, pageMax, fields, single)
	if len(fields) > 0 || err != nil {
		return nil, nil, &FieldError{Code: "validation_failed", Fields: fields}
	}
	arg := store.ListAuditLogParams{ProjectID: pgUUID(project), OwnerID: owner, RowLimit: int32(limit + 1)} //nolint:gosec // limit <= page max
	if v, ok := single("cursor", cursorRaw); ok {
		at, id, err := decodeCursor(v)
		if err != nil {
			return nil, nil, errInvalidCursor
		}
		arg.CursorCreatedAt = pgtype.Timestamptz{Time: at, Valid: true}
		arg.CursorID = pgUUID(id)
	}
	rows, err := store.New(rd.pool).ListAuditLog(ctx, arg)
	if err != nil {
		return nil, nil, fmt.Errorf("list audit log: %w", err)
	}
	if len(rows) == 0 {
		if err := rd.checkProject(ctx, owner, project); err != nil {
			return nil, nil, err
		}
	}
	out := make([]AuditJSON, 0, len(rows))
	for _, r := range rows {
		a := AuditJSON{ID: uuid.UUID(r.ID.Bytes).String(), Action: r.Action, ResourceType: r.ResourceType, CreatedAt: r.CreatedAt.Time.UTC(), Metadata: map[string]string{}}
		if r.ActorUserID.Valid {
			s := r.ActorUserID.String
			a.ActorUserID = &s
		}
		if r.ActorName.Valid {
			s := r.ActorName.String
			a.ActorName = &s
		}
		if r.ResourceID.Valid {
			s := uuid.UUID(r.ResourceID.Bytes).String()
			a.ResourceID = &s
		}
		_ = json.Unmarshal(r.Metadata, &a.Metadata)
		out = append(out, a)
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		last := rows[limit-1]
		c := encodeCursor(last.CreatedAt.Time, last.ID.Bytes)
		next = &c
	}
	return out, next, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	h.listReplays(w, r, false)
}

func (h *Handler) listForEvent(w http.ResponseWriter, r *http.Request) {
	h.listReplays(w, r, true)
}

func (h *Handler) listReplays(w http.ResponseWriter, r *http.Request, forEvent bool) {
	owner, project, event, ok := scope(w, r, forEvent)
	if !ok {
		return
	}
	q := r.URL.Query()
	var ev *uuid.UUID
	if forEvent {
		ev = &event
	}
	items, next, err := h.reads.List(r.Context(), owner, project, ListParams{
		EventID: q["event_id"], State: q["state"], Outcome: q["outcome"], Limit: q["limit"], Cursor: q["cursor"],
	}, ev, h.pageDefault, h.pageMax)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, project, _, ok := scope(w, r, false)
	if !ok {
		return
	}
	id, ok := parseID(chi.URLParam(r, "replayId"))
	if !ok {
		httpx.NotFound(w, r)
		return
	}
	d, err := h.reads.Get(r.Context(), owner, project, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) auditLog(w http.ResponseWriter, r *http.Request) {
	owner, project, _, ok := scope(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	items, next, err := h.reads.Audit(r.Context(), owner, project, q["limit"], q["cursor"], h.pageDefault, h.pageMax)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}
