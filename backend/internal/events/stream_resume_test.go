package events

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/identity/authtest"
)

func lastID(id string) http.Header { return http.Header{"Last-Event-Id": {id}} }

// expectEvents reads event.created frames and checks their ids in order.
func expectEvents(t *testing.T, c *sseConn, want ...uuid.UUID) {
	t.Helper()
	for i, id := range want {
		m := c.next(t)
		if m.Event != "event.created" || m.ID != id.String() {
			t.Fatalf("frame %d = %+v, want event.created %s", i, m, id)
		}
	}
}

func TestResumeSendsMissedEventsInOrder(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	now := time.Now().UTC()
	a.event(t, a.project, a.ep, now.Add(-time.Minute), "POST", nil) // seen long before
	last := a.event(t, a.project, a.ep, now.Add(-40*time.Second), "POST", nil)
	missed := []uuid.UUID{
		a.event(t, a.project, a.ep, now.Add(-30*time.Second), "POST", nil),
		a.event(t, a.project, a.ep, now.Add(-20*time.Second), "PUT", nil),
		a.event(t, a.project, a.ep, now.Add(-10*time.Second), "GET", nil),
	}

	c := a.open(t, a.alice, a.project, lastID(last.String()))
	c.opened(t)
	expectEvents(t, c, missed...)
	live := a.publish(t, a.project, a.ep, "/live")
	expectEvents(t, c, live)
}

func TestResumeOverlapCatchesLateCommits(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	now := time.Now().UTC()
	last := a.event(t, a.project, a.ep, now.Add(-10*time.Second), "POST", nil)
	// Received 2 s before the last event the client saw, but committed after
	// it: the client never got it, and resume must not skip it.
	late := a.event(t, a.project, a.ep, now.Add(-12*time.Second), "POST", nil)

	c := a.open(t, a.alice, a.project, lastID(last.String()))
	c.opened(t)
	expectEvents(t, c, late)
	sentinel := a.publish(t, a.project, a.ep, "/sentinel")
	expectEvents(t, c, sentinel) // and the last-seen event itself is not resent
}

func TestResumeTooManyMissedRequiresResync(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	at := time.Now().UTC().Add(-time.Hour)
	last := a.event(t, a.project, a.ep, at, "POST", nil)
	if _, err := a.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params,
		  headers, body, body_size, body_encoding, correlation_id)
		SELECT gen_random_uuid(), $1, $2, $3::timestamptz + n * interval '1 second', 'POST', '/x', '{}', '[]',
		  '\x', 0, 'utf8', 'test'
		FROM generate_series(1, 600) n`, a.ep, a.project, at); err != nil {
		t.Fatal(err)
	}

	c := a.open(t, a.alice, a.project, lastID(last.String()))
	c.opened(t)
	m := c.next(t)
	if m.Event != "resync.required" || m.Data != `{"reason":"too_many_missed"}` {
		t.Fatalf("frame = %+v, want resync.required too_many_missed", m)
	}
	c.closed(t, 2*time.Second)
}

func TestResumeExactlyAtCapIsReplayed(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	at := time.Now().UTC().Add(-time.Hour)
	last := a.event(t, a.project, a.ep, at, "POST", nil)
	if _, err := a.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params,
		  headers, body, body_size, body_encoding, correlation_id)
		SELECT gen_random_uuid(), $1, $2, $3::timestamptz + n * interval '1 second', 'POST', '/x', '{}', '[]',
		  '\x', 0, 'utf8', 'test'
		FROM generate_series(1, $4::int - 1) n`, a.ep, a.project, at, backfillMax); err != nil {
		t.Fatal(err)
	}
	// With the last-seen event inside the overlap window, the backfill read
	// is exactly backfillMax rows: replayed, not a resync.
	c := a.open(t, a.alice, a.project, lastID(last.String()))
	c.opened(t)
	for i := range backfillMax - 1 {
		if m := c.next(t); m.Event != "event.created" {
			t.Fatalf("frame %d = %+v", i, m)
		}
	}
}

func TestResumeIgnoresInvalidOrForeignIDs(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	authtest.SessionCookie(t, a.DB, "bob") // bob exists
	bobs := a.Project(t, "bob", "Bob's")
	bobsEP := a.endpoint(t, bobs, "Bob endpoint")
	other := a.Project(t, "alice", "Other")
	otherEP := a.endpoint(t, other, "Other endpoint")
	at := time.Now().UTC().Add(-time.Minute)
	bobsEvent := a.event(t, bobs, bobsEP, at, "POST", nil)
	otherProjectEvent := a.event(t, other, otherEP, at, "POST", nil)
	// Events that a valid resume point would replay.
	a.event(t, a.project, a.ep, at.Add(time.Second), "POST", nil)

	for name, h := range map[string]http.Header{
		"malformed":             lastID("not-a-uuid"),
		"braced uuid":           lastID("{" + otherProjectEvent.String() + "}"),
		"unknown":               lastID(uuid.NewString()),
		"other user's event":    lastID(bobsEvent.String()),
		"other project's event": lastID(otherProjectEvent.String()),
	} {
		t.Run(name, func(t *testing.T) {
			c := a.open(t, a.alice, a.project, h)
			c.opened(t)
			live := a.publish(t, a.project, a.ep, "/live")
			expectEvents(t, c, live) // no backfill came first
		})
	}
}

func TestEventCommittedDuringBackfillSentOnce(t *testing.T) {
	during := make(chan uuid.UUID, 1)
	var a *streamAPI
	a = newStreamAPI(t, streamSetup{opts: StreamOptions{afterSubscribe: func() {
		// Commit and notify after the stream subscribed but before backfill
		// reads: the event is both buffered live and found by backfill.
		during <- a.publish(t, a.project, a.ep, "/during")
		time.Sleep(300 * time.Millisecond) // let the notification reach the buffer
	}}})
	last := a.event(t, a.project, a.ep, time.Now().UTC().Add(-time.Minute), "POST", nil)

	c := a.open(t, a.alice, a.project, lastID(last.String()))
	c.opened(t)
	expectEvents(t, c, <-during)
	sentinel := a.publish(t, a.project, a.ep, "/sentinel")
	expectEvents(t, c, sentinel) // not `during` again
}

func TestResumeHeaderWinsOverQueryParameter(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	now := time.Now().UTC()
	old := a.event(t, a.project, a.ep, now.Add(-time.Minute), "POST", nil)
	a.event(t, a.project, a.ep, now.Add(-50*time.Second), "POST", nil)
	newest := a.event(t, a.project, a.ep, now.Add(-40*time.Second), "POST", nil)
	after := a.event(t, a.project, a.ep, now.Add(-30*time.Second), "POST", nil)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		a.url(a.project)+"?last_event_id="+old.String(), nil)
	req.Header.Set("Last-Event-ID", newest.String())
	if got := lastEventID(req); got != newest {
		t.Fatalf("lastEventID = %s, want header value", got)
	}
	// The query parameter alone is a valid resume point too.
	c := a.openURL(t, a.alice, a.url(a.project)+"?last_event_id="+newest.String(), nil)
	c.opened(t)
	expectEvents(t, c, after)
}
