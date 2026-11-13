package replay

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/testutil/isolation"
)

type page struct {
	Items      []ReplayJSON `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// seedReplays inserts n replays a second apart, the newest last; every third
// one is failed with timeout, the others succeeded.
func (e *apiEnv) seedReplays(t *testing.T, event uuid.UUID, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	base := time.Now().Add(-time.Hour)
	for i := range n {
		ids[i] = uuid.Must(uuid.NewV7())
		state, outcome := "succeeded", "succeeded"
		if i%3 == 0 {
			state, outcome = "failed", "timeout"
		}
		if _, err := e.Admin.Exec(context.Background(), `INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, target_url, method, state, outcome, created_at)
			VALUES ($1, $2, $3, 'url', 'https://example.com/', 'POST', $4, $5, $6)`, ids[i], e.project, event, state, outcome, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func (e *apiEnv) replaysPath() string { return "/api/v1/projects/" + e.project.String() + "/replays" }

func TestHistoryNewestFirstWithPages(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	ids := e.seedReplays(t, e.event, 30)
	first := decode[page](t, e.alice.Do(http.MethodGet, e.replaysPath(), ""))
	if len(first.Items) != 25 || first.NextCursor == nil {
		t.Fatalf("first page %d items, cursor %v", len(first.Items), first.NextCursor)
	}
	for i, it := range first.Items {
		if it.ID != ids[29-i].String() {
			t.Fatalf("item %d = %s, want newest first", i, it.ID)
		}
	}
	second := decode[page](t, e.alice.Do(http.MethodGet, e.replaysPath()+"?cursor="+*first.NextCursor, ""))
	if len(second.Items) != 5 || second.NextCursor != nil || second.Items[4].ID != ids[0].String() {
		t.Fatalf("second page %d items", len(second.Items))
	}
	if p := decode[page](t, e.alice.Do(http.MethodGet, e.replaysPath()+"?limit=100", "")); len(p.Items) != 30 {
		t.Fatalf("limit=100: %d", len(p.Items))
	}
	expectError(t, e.alice.Do(http.MethodGet, e.replaysPath()+"?limit=500", ""), 422, "validation_failed")
	expectError(t, e.alice.Do(http.MethodGet, e.replaysPath()+"?limit=0", ""), 422, "validation_failed")
	expectError(t, e.alice.Do(http.MethodGet, e.replaysPath()+"?cursor=not-a-cursor", ""), 400, "invalid_cursor")
}

func TestHistoryFilters(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	e.seedReplays(t, e.event, 9) // 3 failed (timeout), 6 succeeded
	other := e.storeEvent(t, e.project, e.source, [][2]string{}, []byte("x"), "text/plain")
	e.seedReplays(t, other, 2)
	for q, want := range map[string]int{
		"?state=failed":                                 4,
		"?outcome=timeout":                              4,
		"?state=succeeded":                              7,
		"?event_id=" + other.String():                   2,
		"?event_id=" + other.String() + "&state=failed": 1,
	} {
		if p := decode[page](t, e.alice.Do(http.MethodGet, e.replaysPath()+q, "")); len(p.Items) != want {
			t.Errorf("%s: %d items, want %d", q, len(p.Items), want)
		}
	}
	expectError(t, e.alice.Do(http.MethodGet, e.replaysPath()+"?state=done", ""), 422, "validation_failed")
	expectError(t, e.alice.Do(http.MethodGet, e.replaysPath()+"?event_id=x", ""), 422, "validation_failed")
	// Per-event list.
	if p := decode[page](t, e.alice.Do(http.MethodGet, e.eventPath(other), "")); len(p.Items) != 2 {
		t.Fatalf("event replays %d", len(p.Items))
	}
	// An owned project without replays is an empty page.
	empty := e.Project(t, "alice", "Empty")
	if p := decode[page](t, e.alice.Do(http.MethodGet, "/api/v1/projects/"+empty.String()+"/replays", "")); len(p.Items) != 0 {
		t.Fatal("empty project listed replays")
	}
}

func TestReplayDetail(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	id := e.seedReplays(t, e.event, 2)[1]
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.replay_attempts SET response_status = 200, response_body = '<script>alert(1)</script>',
		response_headers = '[["Content-Type","text/html"],["Set-Cookie","[REDACTED]"]]', response_truncated = false, tries = 1, duration_ms = 12,
		header_override_names = '{Authorization}' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	d := decode[ReplayJSON](t, e.alice.Do(http.MethodGet, e.replaysPath()+"/"+id.String(), ""))
	if d.Response == nil || *d.Response.Body.Text != "<script>alert(1)</script>" || d.Response.Body.Encoding != "utf8" ||
		len(d.Response.Headers) != 2 || !d.Response.Headers[1].Redacted || d.Response.Headers[0].Redacted ||
		len(d.HeaderOverrides) != 1 || d.HeaderOverrides[0].Value != "[REDACTED]" || *d.DurationMS != 12 {
		t.Fatalf("detail %+v", d)
	}
}

func TestHistoryIsolation(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	id := e.seedReplays(t, e.event, 1)[0]
	isolation.AssertForeignNotFound(t, e.bob, e.project.String(), []isolation.Route{
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/replays"},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/replays/" + id.String()},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/events/" + e.event.String() + "/replays"},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/audit-log"},
	})
	bobsProject := e.Project(t, "bob", "Bob's")
	isolation.AssertForeignNotFound(t, e.bob, id.String(), []isolation.Route{
		{Method: http.MethodGet, Path: "/api/v1/projects/" + bobsProject.String() + "/replays/{id}"},
	})
	// Alice's replay under her other project.
	other := e.Project(t, "alice", "Other")
	expectError(t, e.alice.Do(http.MethodGet, "/api/v1/projects/"+other.String()+"/replays/"+id.String(), ""), 404, "not_found")
	if p := decode[page](t, e.bob.Do(http.MethodGet, "/api/v1/projects/"+bobsProject.String()+"/replays?event_id="+e.event.String(), "")); len(p.Items) != 0 {
		t.Fatal("bob saw alice's replays through a filter")
	}
}
