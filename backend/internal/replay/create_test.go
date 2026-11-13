package replay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/testutil/fixture"
)

func TestCreateURLReplayQueued(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	listener, err := pgx.Connect(context.Background(), e.DB.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close(context.Background()) }()
	if _, err := listener.Exec(context.Background(), "LISTEN replay_jobs"); err != nil {
		t.Fatal(err)
	}

	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://hooks.example.com/receive?token=abc")))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	r := decode[ReplayJSON](t, rec)
	if r.State != "queued" || r.Target != "https://hooks.example.com/receive?token=abc" || r.Method != http.MethodPost ||
		r.CreatedAt.IsZero() || r.EventID != e.event.String() || r.Outcome != nil {
		t.Fatalf("replay %+v", r)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1 AND state = 'queued' AND actor_user_id = 'alice'`, r.ID); n != 1 {
		t.Fatal("replay row missing")
	}
	if n := e.Count(t, `SELECT count(*) FROM app.jobs WHERE kind = 'replay.deliver' AND state = 'queued' AND payload->>'replay_id' = $1 AND max_attempts = 3`, r.ID); n != 1 {
		t.Fatal("job row missing")
	}
	if n := e.Count(t, `SELECT count(*) FROM app.audit_log WHERE action = 'replay.created' AND resource_id = $1 AND actor_user_id = 'alice'`, r.ID); n != 1 {
		t.Fatal("audit entry missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := listener.WaitForNotification(ctx); err != nil {
		t.Fatalf("no replay_jobs notification: %v", err)
	}
	// It appears in the event's replay list.
	list := decode[struct{ Items []ReplayJSON }](t, e.alice.Do(http.MethodGet, e.eventPath(e.event), ""))
	if len(list.Items) != 1 || list.Items[0].ID != r.ID {
		t.Fatalf("event replays %+v", list.Items)
	}
}

func TestCreateSinkReplayAndMethod(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	in := sinkDest(e.sink)
	in["method"] = "PUT"
	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(in))
	r := decode[ReplayJSON](t, rec)
	if rec.Code != 202 || r.Method != http.MethodPut || r.Destination.Type != "sink" || *r.Destination.EndpointName != "Sink" {
		t.Fatalf("%d %+v", rec.Code, r)
	}
}

func TestCreateIsolation(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	// Bob cannot replay alice's event, through her project or his own.
	bobsProject := e.Project(t, "bob", "Bob's")
	expectError(t, e.bob.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://example.com/"))), 404, "not_found")
	bobPath := "/api/v1/projects/" + bobsProject.String() + "/events/" + e.event.String() + "/replays"
	expectError(t, e.bob.Do(http.MethodPost, bobPath, body(urlDest("https://example.com/"))), 404, "not_found")
	// Alice's event id in the path of her other project.
	other := e.Project(t, "alice", "Other")
	otherPath := "/api/v1/projects/" + other.String() + "/events/" + e.event.String() + "/replays"
	expectError(t, e.alice.Do(http.MethodPost, otherPath, body(urlDest("https://example.com/"))), 404, "not_found")
	// A sink in another of her projects.
	otherSink := e.endpoint(t, other, "Other sink", true)
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(otherSink))), 404, "not_found")
	if n := e.count("app.replay_attempts") + e.count("app.jobs") + e.count("app.audit_log"); n != 0 {
		t.Fatalf("%d rows created by rejected requests", n)
	}
}

func TestCreateRequiresSessionAndSameOrigin(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	send := func(cookie, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, e.eventPath(e.event), strings.NewReader(body(urlDest("https://example.com/"))))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	expired, _ := authtest.Session(t, e.DB, "alice", time.Now().Add(-time.Minute))
	tampered := e.alice.Cookie[:len(e.alice.Cookie)-6] + "AAAA%3D"
	expectError(t, send("", fixture.Origin), 401, "unauthenticated")
	expectError(t, send(tampered, fixture.Origin), 401, "unauthenticated")
	expectError(t, send(expired, fixture.Origin), 401, "session_expired")
	expectError(t, send(e.alice.Cookie, "https://evil.example"), 403, "origin_rejected")
	if n := e.count("app.replay_attempts"); n != 0 {
		t.Fatalf("%d replays created", n)
	}
}

func TestCreateUnavailable(t *testing.T) {
	// REPLAY_ENABLED=false (the production default) refuses URL replay only.
	off := newAPIEnv(t, apiOpts{disabled: true})
	expectError(t, off.alice.Do(http.MethodPost, off.eventPath(off.event), body(urlDest("https://example.com/"))), 503, "replay_unavailable")
	e := newAPIEnv(t, apiOpts{})
	e.status.setActive(false)
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://example.com/"))), 503, "replay_unavailable")
	if n := e.count("app.replay_attempts") + off.count("app.replay_attempts"); n != 0 {
		t.Fatalf("%d replays created while unavailable", n)
	}
	// Test-sink replay never leaves RelayLab: it works with REPLAY_ENABLED off.
	if rec := off.alice.Do(http.MethodPost, off.eventPath(off.event), body(sinkDest(off.sink))); rec.Code != 202 {
		t.Fatalf("sink replay with replay disabled: %d %s", rec.Code, rec.Body)
	}
	// The sink still works without an active guard.
	if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink))); rec.Code != 202 {
		t.Fatalf("sink replay: %d %s", rec.Code, rec.Body)
	}
}

// Without REPLAY_OVERRIDE_ENC_KEY (allowed while URL replay is off), added
// headers cannot be stored, so they are refused as a field error.
func TestOverridesRefusedWithoutKey(t *testing.T) {
	e := newAPIEnv(t, apiOpts{disabled: true, noKey: true})
	in := sinkDest(e.sink)
	in["header_overrides"] = map[string]string{"X-Extra": "1"}
	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(in))
	expectError(t, rec, 422, "validation_failed")
	if !strings.Contains(rec.Body.String(), "Added headers are not available on this server.") {
		t.Fatalf("body %s", rec.Body)
	}
	if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink))); rec.Code != 202 {
		t.Fatalf("sink replay without overrides: %d %s", rec.Code, rec.Body)
	}
}
