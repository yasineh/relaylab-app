package replay

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/platform/config"
)

// fillQueue inserts n non-terminal replays for the project directly.
func (e *apiEnv) fillQueue(t *testing.T, n int, state string) {
	t.Helper()
	for range n {
		if _, err := e.Admin.Exec(context.Background(), `INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, target_url, method, state)
			VALUES ($1, $2, $3, 'url', 'https://example.com/', 'POST', $4)`, uuid.Must(uuid.NewV7()), e.project, e.event, state); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueueFullAtDefaultDepth(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	if e.limits.ProjectQueueDepth != 20 { // the spec default
		t.Fatalf("default depth %d", e.limits.ProjectQueueDepth)
	}
	e.fillQueue(t, 15, "queued")
	e.fillQueue(t, 5, "running")
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://example.com/"))), 429, "replay_queue_full")
	// Terminal replays do not count.
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.replay_attempts SET state = 'succeeded', outcome = 'succeeded'
		WHERE id = (SELECT id FROM app.replay_attempts WHERE state = 'queued' LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://example.com/"))); rec.Code != 202 {
		t.Fatalf("after one finished: %d %s", rec.Code, rec.Body)
	}
}

func TestQueueDepthConfigured(t *testing.T) {
	e := newAPIEnv(t, apiOpts{limits: func(r *config.Replay) { r.ProjectQueueDepth = 5 }})
	for i := range 5 {
		if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://example.com/"+strconv.Itoa(i)))); rec.Code != 202 {
			t.Fatalf("replay %d: %d", i, rec.Code)
		}
	}
	expectError(t, e.alice.Do(http.MethodPost, e.eventPath(e.event), body(urlDest("https://example.com/"))), 429, "replay_queue_full")
	if e.count("app.replay_attempts") != 5 || e.count("app.audit_log") != 5 {
		t.Fatal("rejected request wrote rows")
	}
}

func TestRateLimitedWithRetryAfter(t *testing.T) {
	e := newAPIEnv(t, apiOpts{limiter: NewRateLimiter(2)})
	for range 2 {
		if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink))); rec.Code != 202 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink)))
	expectError(t, rec, 429, "rate_limited")
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 || retry > 30 {
		t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	// Per user: bob's own project is unaffected.
	bobsProject := e.Project(t, "bob", "Bob's")
	ep := e.endpoint(t, bobsProject, "B", true)
	ev := e.storeEvent(t, bobsProject, ep, [][2]string{}, []byte("x"), "text/plain")
	sink := e.endpoint(t, bobsProject, "S", true)
	path := "/api/v1/projects/" + bobsProject.String() + "/events/" + ev.String() + "/replays"
	if rec := e.bob.Do(http.MethodPost, path, body(sinkDest(sink))); rec.Code != 202 {
		t.Fatalf("bob: %d %s", rec.Code, rec.Body)
	}
	// Previews are not rate limited.
	if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event)+"/preview", body(sinkDest(e.sink))); rec.Code != 200 {
		t.Fatalf("preview: %d", rec.Code)
	}
}

func TestRateLimiterRefills(t *testing.T) {
	l := NewRateLimiter(60) // one per second after the burst
	for range 60 {
		if ok, _ := l.Allow("u"); !ok {
			t.Fatal("burst refused")
		}
	}
	ok, wait := l.Allow("u")
	if ok || wait <= 0 || wait > 2*time.Second {
		t.Fatalf("over the limit: %v %s", ok, wait)
	}
}
