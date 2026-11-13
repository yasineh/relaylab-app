package events

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/platform/redact"
	"relaylab/internal/testutil/isolation"
)

type deleteAPI struct {
	*seedEnv
	svc        *Service
	alice, bob isolation.Requester
}

func newDeleteAPI(t *testing.T) *deleteAPI {
	t.Helper()
	e := newSeedEnv(t)
	svc := NewService(e.Pool, redact.New(nil, nil))
	h := e.Router(func(v1 chi.Router) {
		v1.Route("/projects/{projectId}/events", NewHandler(svc, 50, 100).Routes)
		v1.Route("/projects/{projectId}/endpoints", NewHandler(svc, 50, 100).EndpointRoutes)
	})
	return &deleteAPI{seedEnv: e, svc: svc, alice: e.As(t, h, "alice"), bob: e.As(t, h, "bob")}
}

func (a *deleteAPI) replay(t *testing.T, project, event uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.Admin.Exec(context.Background(), `INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, method)
		VALUES ($1, $2, $3, 'sink', 'POST')`, id, project, event); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *deleteAPI) events(t *testing.T, project, endpoint uuid.UUID, n int) {
	t.Helper()
	if _, err := a.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params, headers,
		  body, body_size, body_encoding, correlation_id)
		SELECT gen_random_uuid(), $1, $2, now(), 'POST', '', '{}', '[]', '', 0, 'utf8', 'req'
		FROM generate_series(1, $3::int)`, endpoint, project, n); err != nil {
		t.Fatal(err)
	}
}

func eventPath(project, id uuid.UUID) string {
	return "/api/v1/projects/" + project.String() + "/events/" + id.String()
}

func clearPath(project, endpoint uuid.UUID) string {
	return "/api/v1/projects/" + project.String() + "/endpoints/" + endpoint.String() + "/events"
}

// 3.1
func TestDeleteEvent(t *testing.T) {
	a := newDeleteAPI(t)
	ep := a.endpoint(t, a.project, "Stripe")
	id := a.event(t, a.project, ep, time.Now(), "POST", nil)
	keep := a.event(t, a.project, ep, time.Now(), "POST", nil)
	r1, r2 := a.replay(t, a.project, id), a.replay(t, a.project, keep)

	// Another user: 404, and the event remains.
	if rec := a.bob.Do(http.MethodDelete, eventPath(a.project, id), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob: %d", rec.Code)
	}
	// The event under a different project of the same owner: 404.
	other := a.Project(t, "alice", "Other")
	if rec := a.alice.Do(http.MethodDelete, eventPath(other, id), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("mismatched project: %d", rec.Code)
	}
	if rec := a.alice.Do(http.MethodDelete, "/api/v1/projects/"+a.project.String()+"/events/not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id: %d", rec.Code)
	}
	if a.Count(t, `SELECT count(*) FROM app.webhook_events WHERE id = $1`, id) != 1 {
		t.Fatal("event deleted by a refused request")
	}

	rec := a.alice.Do(http.MethodDelete, eventPath(a.project, id), "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("owner: %d %s", rec.Code, rec.Body)
	}
	if a.Count(t, `SELECT count(*) FROM app.webhook_events WHERE id = $1`, id) != 0 ||
		a.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1`, r1) != 0 {
		t.Fatal("event or its replay remains")
	}
	if a.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1`, r2) != 1 {
		t.Fatal("another event's replay deleted")
	}
	if rec := a.alice.Do(http.MethodGet, eventPath(a.project, id), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("detail after delete: %d", rec.Code)
	}
	if rec := a.alice.Do(http.MethodDelete, eventPath(a.project, id), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
}

// 3.2
func TestClearEndpointEvents(t *testing.T) {
	a := newDeleteAPI(t)
	e1 := a.endpoint(t, a.project, "E1")
	e2 := a.endpoint(t, a.project, "E2")
	a.events(t, a.project, e1, 120)
	a.events(t, a.project, e2, 7)

	if rec := a.bob.Do(http.MethodDelete, clearPath(a.project, e1), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob: %d", rec.Code)
	}
	other := a.Project(t, "alice", "Other")
	if rec := a.alice.Do(http.MethodDelete, clearPath(other, e1), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("mismatched project: %d", rec.Code)
	}
	if rec := a.alice.Do(http.MethodDelete, clearPath(a.project, uuid.Must(uuid.NewV7())), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown endpoint: %d", rec.Code)
	}
	if a.Count(t, `SELECT count(*) FROM app.webhook_events WHERE endpoint_id = $1`, e1) != 120 {
		t.Fatal("events deleted by a refused request")
	}

	rec := a.alice.Do(http.MethodDelete, clearPath(a.project, e1), "")
	if rec.Code != http.StatusOK || rec.Body.String() != `{"deleted":120}`+"\n" {
		t.Fatalf("clear: %d %q", rec.Code, rec.Body)
	}
	if a.Count(t, `SELECT count(*) FROM app.webhook_events WHERE endpoint_id = $1`, e1) != 0 ||
		a.Count(t, `SELECT count(*) FROM app.webhook_events WHERE endpoint_id = $1`, e2) != 7 {
		t.Fatal("wrong events deleted")
	}
	// Nothing left: deleted 0.
	if rec := a.alice.Do(http.MethodDelete, clearPath(a.project, e1), ""); rec.Body.String() != `{"deleted":0}`+"\n" {
		t.Fatalf("second clear: %q", rec.Body)
	}
}

// A clear past its time budget returns the count so far; repeating finishes.
func TestClearEndpointResumesAfterBudget(t *testing.T) {
	a := newDeleteAPI(t)
	ep := a.endpoint(t, a.project, "E")
	a.events(t, a.project, ep, 2500)
	a.svc.clearLimit = time.Nanosecond // stop after the first batch
	n, err := a.svc.ClearEndpoint(context.Background(), "alice", a.project, ep)
	if err != nil || n != 1000 {
		t.Fatalf("first call: %d %v", n, err)
	}
	var total = n
	for n > 0 {
		if n, err = a.svc.ClearEndpoint(context.Background(), "alice", a.project, ep); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	if total != 2500 || a.Count(t, `SELECT count(*) FROM app.webhook_events`) != 0 {
		t.Fatalf("total %d", total)
	}
}
