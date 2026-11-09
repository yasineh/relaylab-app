package ingestion

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/endpoints/token"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
	"relaylab/internal/platform/redact"
)

// dbTimeout bounds the token lookup and the event write; exceeding it is a 503.
const dbTimeout = 3 * time.Second

var allowedMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true,
	http.MethodDelete: true, http.MethodHead: true, http.MethodOptions: true,
}

const allowHeader = "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS"

// Hasher derives the token lookup hash; *token.Keys implements it.
type Hasher interface{ Hash(token string) []byte }

// Options configures a Handler. Limits come from config.Limits.
type Options struct {
	Store           Store
	Hasher          Hasher
	Redactor        *redact.Redactor
	Metrics         *Metrics
	MaxBodyBytes    int
	MaxHeaderFields int
	BodyTimeout     time.Duration
	// Per-instance token buckets.
	EndpointPerMinute, EndpointBurst int
	IPPerMinute, IPBurst             int
	// IngestHost, when set, is the only Host ingestion answers on (a separate
	// ingest domain). Empty means ingestion shares the app host.
	IngestHost string
	Now        func() time.Time
}

// Handler serves /h/{token} and /h/{token}/*. It never reads cookies or
// sessions and never exposes stored events.
type Handler struct {
	o          Options
	endpointRL *keyedLimiter
	ipRL       *keyedLimiter
}

// NewHandler returns an ingestion handler.
func NewHandler(o Options) *Handler {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Handler{
		o:          o,
		endpointRL: newKeyedLimiter(o.EndpointPerMinute, o.EndpointBurst),
		ipRL:       newKeyedLimiter(o.IPPerMinute, o.IPBurst),
	}
}

// Routes mounts the ingestion routes on r (the root router). Methods chi does
// not know (such as PROPFIND) are rejected by the root router's 405 handler
// before reaching ingestion.
func (h *Handler) Routes(r chi.Router) {
	r.Handle("/h/{token}", h)
	r.Handle("/h/{token}/*", h)
}

// result is what one request did, for the response, log, and metrics.
type result struct {
	status     int
	outcome    string
	code, msg  string
	retryAfter int
	endpointID uuid.UUID
	eventID    uuid.UUID
	bodySize   int
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := h.o.Now()
	res := h.handle(w, r, start)
	h.respond(w, r, res)

	elapsed := h.o.Now().Sub(start)
	if h.o.Metrics != nil {
		h.o.Metrics.requests.WithLabelValues(res.outcome).Inc()
		h.o.Metrics.duration.Observe(elapsed.Seconds())
		if res.outcome == outcomeAccepted {
			h.o.Metrics.bodySize.Observe(float64(res.bodySize))
		}
	}
	// Never the token, path, query, headers, or body: only safe metadata.
	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("outcome", res.outcome),
		slog.Int("status", res.status),
		slog.Int("body_bytes", res.bodySize),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	}
	if res.endpointID != uuid.Nil {
		attrs = append(attrs, slog.String("endpoint_id", res.endpointID.String()))
	}
	if res.eventID != uuid.Nil {
		attrs = append(attrs, slog.String("event_id", res.eventID.String()))
	}
	level := slog.LevelInfo
	if res.outcome == outcomeError {
		level = slog.LevelError
	}
	logging.FromContext(r.Context()).LogAttrs(r.Context(), level, "ingest_request", attrs...)
}

func reject(status int, outcome, code, msg string) result {
	return result{status: status, outcome: outcome, code: code, msg: msg}
}

var (
	errNotFound = reject(http.StatusNotFound, outcomeNotFound, "endpoint_not_found", "No endpoint exists at this URL.")
	errDisabled = reject(http.StatusGone, outcomeDisabled, "endpoint_disabled", "This endpoint is disabled.")
	errTooLarge = reject(http.StatusRequestEntityTooLarge, outcomeRejectedSize, "payload_too_large", "The request body is too large.")
	errHeaders  = reject(http.StatusRequestHeaderFieldsTooLarge, outcomeRejectedSize, "headers_too_large", "Request headers are too large.")
	errURI      = reject(http.StatusRequestURITooLong, outcomeRejectedSize, "uri_too_long", "The request path or query string is too long.")
	errTimeout  = reject(http.StatusRequestTimeout, outcomeRejectedTimeout, "request_timeout", "The request body was not received in time.")
	errBadBody  = reject(http.StatusBadRequest, outcomeRejectedInvalid, "invalid_request", "The request body could not be read.")
	errMethod   = reject(http.StatusMethodNotAllowed, outcomeRejectedMethod, "method_not_allowed", "This method is not supported.")
	errStore    = reject(http.StatusServiceUnavailable, outcomeError, "unavailable", "The webhook could not be stored. Try again.")
)

func rateLimited(after int) result {
	res := reject(http.StatusTooManyRequests, outcomeRejectedRate, "rate_limited", "Too many requests. Try again later.")
	res.retryAfter = after
	return res
}

// handle runs the checks in order (design: Routing and middleware). Every
// rejection happens before anything is stored.
func (h *Handler) handle(w http.ResponseWriter, r *http.Request, start time.Time) result {
	if !allowedMethods[r.Method] {
		return errMethod
	}
	if h.o.IngestHost != "" && !strings.EqualFold(hostOnly(r.Host), h.o.IngestHost) {
		return errNotFound
	}
	if headerFieldCount(r) > h.o.MaxHeaderFields {
		return errHeaders
	}

	tok := chi.URLParam(r, "token")
	if !token.WellFormed(tok) {
		return errNotFound // garbage never reaches the database
	}

	clientIP, haveIP := httpx.ClientIP(r.Context())
	if haveIP {
		if ok, after := h.ipRL.allow(clientIP.String(), start); !ok {
			return rateLimited(after)
		}
	}
	if r.ContentLength > int64(h.o.MaxBodyBytes) {
		return errTooLarge // declared too large: rejected before reading
	}

	lookupCtx, cancel := context.WithTimeout(r.Context(), dbTimeout)
	ep, err := h.o.Store.Lookup(lookupCtx, h.o.Hasher.Hash(tok))
	cancel()
	switch {
	case errors.Is(err, ErrUnknownEndpoint):
		return errNotFound
	case err != nil:
		logging.FromContext(r.Context()).Error("endpoint lookup failed", slog.String("error", err.Error()))
		return errStore
	}
	if !ep.Enabled {
		res := errDisabled
		res.endpointID = ep.ID
		return res
	}
	if ok, after := h.endpointRL.allow(ep.ID.String(), start); !ok {
		res := rateLimited(after)
		res.endpointID = ep.ID
		return res
	}

	body, bodyErr := h.readBody(w, r)
	if bodyErr != nil {
		res := bodyErr.(bodyError).res
		res.endpointID = ep.ID
		return res
	}

	prefix := "/h/" + tok
	c, ok := capture(r, strings.TrimPrefix(r.URL.Path, prefix), strings.TrimPrefix(r.URL.EscapedPath(), prefix),
		h.o.Redactor, clientIP, haveIP)
	if !ok {
		res := errURI
		res.endpointID = ep.ID
		return res
	}

	contentType := ""
	if c.ContentType != nil {
		contentType = *c.ContentType
	}
	ev := event{
		ID:         uuid.Must(uuid.NewV7()),
		Endpoint:   ep,
		ReceivedAt: start.UTC(),
		Captured:   c,
		Body:       body,
		Class:      classify(contentType, body),
		RequestID:  httpx.RequestID(r.Context()),
	}
	saveCtx, cancelSave := context.WithTimeout(r.Context(), dbTimeout)
	err = h.o.Store.Save(saveCtx, ev)
	cancelSave()
	if err != nil {
		logging.FromContext(r.Context()).Error("event write failed",
			slog.String("endpoint_id", ep.ID.String()), slog.String("error", err.Error()))
		res := errStore
		res.endpointID = ep.ID
		return res
	}

	// The event is durable. A failed notification only delays live updates;
	// the explorer recovers from the database.
	notifyCtx, cancelNotify := context.WithTimeout(context.WithoutCancel(r.Context()), dbTimeout)
	if err := h.o.Store.Notify(notifyCtx, ep.ProjectID, ev.ID); err != nil {
		logging.FromContext(r.Context()).Warn("event notification failed",
			slog.String("event_id", ev.ID.String()), slog.String("error", err.Error()))
	}
	cancelNotify()

	return result{status: http.StatusOK, outcome: outcomeAccepted, endpointID: ep.ID, eventID: ev.ID, bodySize: len(body)}
}

type bodyError struct{ res result }

func (e bodyError) Error() string { return e.res.code }

// readBody reads the whole body within the size and time limits. Bodies of
// exactly MaxBodyBytes are accepted.
func (h *Handler) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(h.o.BodyTimeout)) // unsupported in some test writers
	defer func() { _ = rc.SetReadDeadline(time.Time{}) }()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(h.o.MaxBodyBytes)))
	if err == nil {
		return body, nil
	}
	var tooLarge *http.MaxBytesError
	var netErr net.Error
	switch {
	case errors.As(err, &tooLarge):
		return nil, bodyError{errTooLarge}
	case errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return nil, bodyError{errTimeout}
	default:
		return nil, bodyError{errBadBody}
	}
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, res result) {
	w.Header().Set("Cache-Control", "no-store")
	if res.status == http.StatusOK {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "event_id": res.eventID.String()})
		return
	}
	switch res.status {
	case http.StatusMethodNotAllowed:
		w.Header().Set("Allow", allowHeader)
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", strconv.Itoa(res.retryAfter))
	case http.StatusRequestEntityTooLarge, http.StatusRequestTimeout, http.StatusBadRequest:
		// The unread remainder of the body may not be drainable.
		w.Header().Set("Connection", "close")
	}
	httpx.WriteError(w, r, res.status, res.code, res.msg)
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
