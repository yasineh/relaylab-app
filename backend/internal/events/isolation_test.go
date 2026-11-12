package events

import (
	"net/http"
	"testing"
	"time"

	"relaylab/internal/testutil/isolation"
)

// TestIsolationAcrossUsers: bob gets the same 404 for alice's project and
// events as for ids that do not exist, on every event route.
func TestIsolationAcrossUsers(t *testing.T) {
	a := newAPI(t)
	ev := a.event(t, a.project, a.ep, time.Now().UTC(), "POST", strp("application/json"))
	bobsProject := a.Project(t, "bob", "Bob's")

	// Alice's project in the path.
	isolation.AssertForeignNotFound(t, a.bob, a.project.String(), []isolation.Route{
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/events"},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/events?endpoint_id=" + a.ep.String()},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/events/" + ev.String()},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/events/" + ev.String() + "/body"},
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/events/stream"},
	})
	// Alice's event id under alice's project and under bob's own project.
	isolation.AssertForeignNotFound(t, a.bob, ev.String(), []isolation.Route{
		{Method: http.MethodGet, Path: a.base(a.project) + "/{id}"},
		{Method: http.MethodGet, Path: a.base(a.project) + "/{id}/body"},
		{Method: http.MethodGet, Path: a.base(bobsProject) + "/{id}"},
		{Method: http.MethodGet, Path: a.base(bobsProject) + "/{id}/body"},
	})
	// Filtering bob's own project by alice's endpoint returns nothing of hers.
	rec := a.bob.Do(http.MethodGet, a.base(bobsProject)+"?endpoint_id="+a.ep.String(), "")
	if p := decodeJSON[listBody](t, rec.Body.Bytes()); rec.Code != 200 || len(p.Items) != 0 {
		t.Fatalf("bob filtering by alice's endpoint: %d %s", rec.Code, rec.Body)
	}
	// Alice still sees her event.
	if p := decodeJSON[listBody](t, a.alice.Do(http.MethodGet, a.base(a.project), "").Body.Bytes()); len(p.Items) != 1 {
		t.Fatalf("alice sees %d events", len(p.Items))
	}
}

// TestIsolationAcrossProjects: one owner's projects do not leak into each
// other through the path, the endpoint filter, or a cursor.
func TestIsolationAcrossProjects(t *testing.T) {
	a := newAPI(t)
	y := a.Project(t, "alice", "Y")
	epY := a.endpoint(t, y, "Y endpoint")
	evX := a.event(t, a.project, a.ep, time.Now().UTC(), "POST", nil)
	evY := a.event(t, y, epY, time.Now().UTC().Add(time.Millisecond), "POST", nil)

	rec := a.alice.Do(http.MethodGet, a.base(a.project)+"?endpoint_id="+epY.String(), "")
	if p := decodeJSON[listBody](t, rec.Body.Bytes()); rec.Code != 200 || len(p.Items) != 0 {
		t.Fatalf("project X filtered by Y's endpoint: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{a.base(a.project) + "/" + evY.String(), a.base(a.project) + "/" + evY.String() + "/body",
		a.base(y) + "/" + evX.String(), a.base(y) + "/" + evX.String() + "/body"} {
		if rec := a.alice.Do(http.MethodGet, path, ""); rec.Code != 404 {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
	// A cursor from project Y used on project X returns only X's events.
	a.event(t, y, epY, time.Now().UTC().Add(2*time.Millisecond), "POST", nil)
	first := decodeJSON[listBody](t, a.alice.Do(http.MethodGet, a.base(y)+"?limit=1", "").Body.Bytes())
	if first.NextCursor == nil {
		t.Fatal("expected a cursor")
	}
	rec = a.alice.Do(http.MethodGet, a.base(a.project)+"?limit=1&cursor="+*first.NextCursor, "")
	for _, it := range decodeJSON[listBody](t, rec.Body.Bytes()).Items {
		if it.EndpointID != a.ep.String() {
			t.Fatalf("event %s from project Y returned in X", it.ID)
		}
	}
}
