package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/identity"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
	"relaylab/internal/realtime"
)

// SSE event names (live-event-stream spec).
const (
	sseEventCreated   = "event.created"
	sseResyncRequired = "resync.required"
	sseSessionExpired = "session.expired"
)

// Resync reasons sent in resync.required data, for clients and debugging.
const (
	resyncSlowClient  = "slow_client"       // the subscriber buffer filled up
	resyncReconnected = "listener_restart"  // the instance's LISTEN connection was re-established
	resyncTooMany     = "too_many_missed"   // more than backfillMax events were missed
	resyncProjectGone = "project_not_found" // the project was deleted or is no longer the user's
)

// retryMillis is sent as `retry:` on connect: the browser's reconnect delay.
const retryMillis = 3000

// Resume (live-event-stream spec, "Resume without gaps").
const (
	// backfillMax is the most missed events a reconnect replays; beyond it
	// the client must reload its list (resync.required).
	backfillMax = 500
	// backfillOverlap re-reads events received shortly before the last one
	// the client saw: ids are generated before commit, so an event with an
	// earlier time can commit (and notify) after a later one.
	backfillOverlap = 5 * time.Second
)

// liveBatchMax bounds how many buffered ids one summary query loads.
const liveBatchMax = 64

// SessionValidator re-checks the request's session for the life of a stream.
type SessionValidator interface {
	Validate(ctx context.Context, r *http.Request) (identity.Identity, error)
}

// StreamOptions configure GET /projects/{projectId}/events/stream.
type StreamOptions struct {
	Broker *realtime.Broker
	// Slots caps concurrent streams per user (LIMIT_STREAMS_PER_USER).
	Slots     *realtime.Slots
	Validator SessionValidator
	// RevalidateEvery is how often the session and project are re-checked.
	RevalidateEvery time.Duration
	// Heartbeat is the interval between `: ping` comments.
	Heartbeat time.Duration
	// afterSubscribe runs between subscribing and backfill (tests only).
	afterSubscribe func()
	// WriteTimeout bounds each write; a client that cannot take a message
	// within it is disconnected.
	WriteTimeout time.Duration
	// Shutdown, when closed, ends every open stream so graceful shutdown is
	// not held up. Clients reconnect (to another instance) and resume.
	Shutdown <-chan struct{}
}

// WithStream enables the live event stream route.
func (h *Handler) WithStream(o StreamOptions) *Handler {
	if o.RevalidateEvery <= 0 {
		o.RevalidateEvery = 60 * time.Second
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 20 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 30 * time.Second
	}
	h.streamOpts = &o
	return h
}

// sseWriter writes SSE frames, each under its own write deadline, and
// flushes after every frame.
type sseWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
}

func (s *sseWriter) write(b []byte) error {
	if err := s.rc.SetWriteDeadline(time.Now().Add(s.timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	return s.rc.Flush()
}

// send writes one message. data is encoded with encoding/json on a single
// line: newlines and other control characters inside received content are
// escaped, so they can never start a new SSE line or field.
func (s *sseWriter) send(event, id string, data any) error {
	j, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	b.WriteString("event: " + event + "\ndata: ")
	b.Write(j)
	b.WriteString("\n\n")
	return s.write(b.Bytes())
}

type reasonJSON struct {
	Reason string `json:"reason"`
}

// stream serves the live event stream for one project. It must be mounted
// behind identity.RequireSession, which answers 401 for missing, invalid,
// expired, and revoked sessions before the stream opens.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	o := h.streamOpts
	owner, projectID, _, ok := h.scope(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.svc.CheckProject(ctx, owner, projectID); err != nil {
		h.fail(w, r, err)
		return
	}

	if !o.Slots.Acquire(owner) {
		httpx.WriteError(w, r, http.StatusTooManyRequests, "too_many_streams",
			"Too many live connections are open. Close another RelayLab tab and try again.")
		return
	}
	defer o.Slots.Release(owner)

	// Subscribe before anything is read or sent, so no event committed from
	// here on can be missed.
	sub := o.Broker.Subscribe(projectID)
	defer sub.Close()

	rc := http.NewResponseController(w)
	// The server's write timeout suits short requests; a stream instead sets
	// a deadline per write (sseWriter). net/http already clears the read
	// deadline once a body-less request is read; clearing it here keeps that
	// explicit.
	_ = rc.SetReadDeadline(time.Time{})
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	out := &sseWriter{w: w, rc: rc, timeout: o.WriteTimeout}
	log := logging.FromContext(ctx).With(slog.String("project_id", projectID.String()))

	if err := out.write([]byte("retry: " + strconv.Itoa(retryMillis) + "\n\n")); err != nil {
		return
	}
	seen := newSeenSet()
	if o.afterSubscribe != nil {
		o.afterSubscribe()
	}
	switch err := h.backfill(ctx, out, seen, owner, projectID, lastEventID(r)); {
	case errors.Is(err, errTooManyMissed):
		_ = out.send(sseResyncRequired, "", reasonJSON{Reason: resyncTooMany})
		return
	case err != nil:
		if ctx.Err() == nil {
			log.Warn("event stream backfill failed", slog.String("error", err.Error()))
		}
		return
	}

	heartbeat := time.NewTicker(o.Heartbeat)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(o.RevalidateEvery)
	defer revalidate.Stop()
	session, _ := identity.FromContext(ctx)
	expiry := time.NewTimer(time.Until(session.ExpiresAt))
	defer expiry.Stop()

	// recheck re-runs the REST API's session validator and the ownership
	// check. It reports false after telling the client why the stream ends.
	recheck := func() bool {
		v, err := o.Validator.Validate(ctx, r)
		switch {
		case errors.Is(err, identity.ErrUnauthenticated), errors.Is(err, identity.ErrSessionExpired):
			_ = out.send(sseSessionExpired, "", reasonJSON{Reason: "session_ended"})
			return false
		case err != nil:
			if ctx.Err() == nil {
				log.Warn("event stream session check failed", slog.String("error", err.Error()))
			}
			return false // the client reconnects and gets a definite answer
		case v.UserID != owner:
			_ = out.send(sseSessionExpired, "", reasonJSON{Reason: "session_ended"})
			return false
		}
		if err := h.svc.CheckProject(ctx, owner, projectID); errors.Is(err, ErrNotFound) {
			_ = out.send(sseResyncRequired, "", reasonJSON{Reason: resyncProjectGone})
			return false
		} else if err != nil {
			if ctx.Err() == nil {
				log.Warn("event stream project check failed", slog.String("error", err.Error()))
			}
			return false
		}
		// Better Auth may have extended the session (updateAge).
		expiry.Reset(time.Until(v.ExpiresAt))
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-o.Shutdown:
			return
		case <-sub.Dropped():
			reason := resyncSlowClient
			if sub.Reason() == realtime.DropReconnect {
				reason = resyncReconnected
			}
			_ = out.send(sseResyncRequired, "", reasonJSON{Reason: reason})
			return
		case id := <-sub.Events():
			ids := drain(sub, id)
			if err := h.sendLive(ctx, out, seen, owner, projectID, ids); err != nil {
				if ctx.Err() == nil {
					log.Warn("event stream closed", slog.String("error", err.Error()))
				}
				return
			}
		case <-heartbeat.C:
			if err := out.write([]byte(": ping\n\n")); err != nil {
				return
			}
		case <-revalidate.C:
			if !recheck() {
				return
			}
		case <-expiry.C:
			// Re-validate rather than trust the expiry seen at connect.
			if !recheck() {
				return
			}
		}
	}
}

var errTooManyMissed = errors.New("too many missed events")

// lastEventID returns the resume point: the Last-Event-ID header that
// EventSource sends when it reconnects, else the last_event_id query
// parameter (for a first connection). The header wins because it is the
// newest id the browser received; the query parameter stays at its original
// value across reconnects. Anything that is not a UUID is ignored.
func lastEventID(r *http.Request) uuid.UUID {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	id, err := uuid.Parse(raw)
	if err != nil || len(raw) != len(id.String()) {
		return uuid.Nil
	}
	return id
}

// backfill sends the events received after last, oldest first, before any
// live event. The subscription already exists, so an event committed during
// backfill is both read here and buffered live; the seen set sends it once.
// An id that is not an event in owner's project is ignored (live only).
func (h *Handler) backfill(ctx context.Context, out *sseWriter, seen *seenSet, owner string, project, last uuid.UUID) error {
	if last == uuid.Nil {
		return nil
	}
	at, err := h.svc.ReceivedAt(ctx, owner, project, last)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	items, err := h.svc.ReceivedSince(ctx, owner, project, at.Add(-backfillOverlap), backfillMax+1)
	if err != nil {
		return err
	}
	if len(items) > backfillMax {
		return errTooManyMissed
	}
	seen.add(last) // the client has it
	return sendSummaries(out, seen, items)
}

// drain collects first plus whatever else is already buffered, up to liveBatchMax.
func drain(sub *realtime.Subscription, first uuid.UUID) []uuid.UUID {
	ids := []uuid.UUID{first}
	for len(ids) < liveBatchMax {
		select {
		case id := <-sub.Events():
			ids = append(ids, id)
		default:
			return ids
		}
	}
	return ids
}

// sendLive loads and sends the summaries for ids not already sent.
func (h *Handler) sendLive(ctx context.Context, out *sseWriter, seen *seenSet, owner string, project uuid.UUID, ids []uuid.UUID) error {
	fresh := ids[:0]
	for _, id := range ids {
		if !seen.has(id) {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	items, err := h.svc.Summaries(ctx, owner, project, fresh)
	if err != nil {
		return err
	}
	return sendSummaries(out, seen, items)
}

func sendSummaries(out *sseWriter, seen *seenSet, items []Summary) error {
	for _, s := range items {
		if seen.has(s.ID) {
			continue
		}
		if err := out.send(sseEventCreated, s.ID.String(), toSummary(s)); err != nil {
			return err
		}
		seen.add(s.ID)
	}
	return nil
}

// seenSetSize bounds the per-connection record of sent ids.
const seenSetSize = 1000

// seenSet remembers the most recently sent ids so an event is sent at most
// once per connection, even when backfill and live delivery overlap.
type seenSet struct {
	ring []uuid.UUID
	next int
	m    map[uuid.UUID]struct{}
}

func newSeenSet() *seenSet {
	return &seenSet{ring: make([]uuid.UUID, 0, seenSetSize), m: make(map[uuid.UUID]struct{}, seenSetSize)}
}

func (s *seenSet) has(id uuid.UUID) bool { _, ok := s.m[id]; return ok }

func (s *seenSet) add(id uuid.UUID) {
	if s.has(id) {
		return
	}
	if len(s.ring) < seenSetSize {
		s.ring = append(s.ring, id)
	} else {
		delete(s.m, s.ring[s.next])
		s.ring[s.next] = id
		s.next = (s.next + 1) % seenSetSize
	}
	s.m[id] = struct{}{}
}
