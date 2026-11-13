package replay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"relaylab/internal/testutil/fixture"
)

func (e *apiEnv) createWithKey(key string, in map[string]any) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, e.eventPath(e.event), strings.NewReader(body(in)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", fixture.Origin)
	req.Header.Set("Cookie", e.alice.Cookie)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestIdempotentDoubleSubmit(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	in := urlDest("https://hooks.example.com/a")
	first := e.createWithKey("key-1", in)
	second := e.createWithKey("key-1", in)
	if first.Code != 202 || second.Code != 200 {
		t.Fatalf("codes %d %d", first.Code, second.Code)
	}
	if decode[ReplayJSON](t, first).ID != decode[ReplayJSON](t, second).ID {
		t.Fatal("different replays for one key")
	}
	if e.count("app.replay_attempts") != 1 || e.count("app.jobs") != 1 || e.count("app.audit_log") != 1 {
		t.Fatal("duplicate rows for one key")
	}
	// Without a key, each request is a new replay.
	e.createWithKey("", in)
	if e.count("app.replay_attempts") != 2 {
		t.Fatal("keyless request deduplicated")
	}
}

func TestIdempotencyKeyReusedWithDifferentBody(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	e.createWithKey("key-1", urlDest("https://hooks.example.com/a"))
	expectError(t, e.createWithKey("key-1", urlDest("https://hooks.example.com/b")), 409, "idempotency_conflict")
	withMethod := urlDest("https://hooks.example.com/a")
	withMethod["method"] = "PUT"
	expectError(t, e.createWithKey("key-1", withMethod), 409, "idempotency_conflict")
	if e.count("app.replay_attempts") != 1 {
		t.Fatal("conflicting request created a replay")
	}
}

func TestIdempotencyKeyLimitsAndExpiry(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	expectError(t, e.createWithKey(strings.Repeat("k", 129), urlDest("https://hooks.example.com/a")), 422, "validation_failed")
	if rec := e.createWithKey(strings.Repeat("k", 128), urlDest("https://hooks.example.com/a")); rec.Code != 202 {
		t.Fatalf("128-character key: %d", rec.Code)
	}
	// After 24 hours the key may be used again, even for a different request.
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.replay_attempts SET created_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if rec := e.createWithKey(strings.Repeat("k", 128), urlDest("https://hooks.example.com/b")); rec.Code != 202 {
		t.Fatalf("expired key reuse: %d %s", rec.Code, rec.Body)
	}
	if e.count("app.replay_attempts") != 2 {
		t.Fatal("expired key did not create a new replay")
	}
	// Keys are per project: another project may use the same key.
	other := e.Project(t, "alice", "Other")
	ep := e.endpoint(t, other, "E", true)
	ev := e.storeEvent(t, other, ep, [][2]string{}, []byte("x"), "text/plain")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/projects/"+other.String()+"/events/"+ev.String()+"/replays", strings.NewReader(body(urlDest("https://hooks.example.com/a"))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", fixture.Origin)
	req.Header.Set("Cookie", e.alice.Cookie)
	req.Header.Set("Idempotency-Key", strings.Repeat("k", 128))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatalf("same key in another project: %d %s", rec.Code, rec.Body)
	}
}

func TestConcurrentSubmitsWithOneKey(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	in := urlDest("https://hooks.example.com/a")
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Go(func() { codes[i] = e.createWithKey("race", in).Code })
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case 202:
			created++
		case 200:
		default:
			t.Fatalf("codes %v", codes)
		}
	}
	if created != 1 || e.count("app.replay_attempts") != 1 || e.count("app.jobs") != 1 {
		t.Fatalf("codes %v, replays %d", codes, e.count("app.replay_attempts"))
	}
}
