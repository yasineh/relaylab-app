package events

import (
	"context"
	"crypto/rand"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/testutil/fixture"
)

type seedEnv struct {
	*fixture.Env
	q       store.Querier
	project uuid.UUID
}

func newSeedEnv(t *testing.T) *seedEnv {
	t.Helper()
	fx := fixture.New(t, "alice", "bob")
	return &seedEnv{Env: fx, q: newQuerier(fx.Pool), project: fx.Project(t, "alice", "Alpha")}
}

func (e *seedEnv) endpoint(t *testing.T, project uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	hash := make([]byte, 32)
	_, _ = rand.Read(hash)
	if _, err := e.Admin.Exec(context.Background(),
		`INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext) VALUES ($1, $2, $3, $4, '\x00')`,
		id, project, name, hash); err != nil {
		t.Fatal(err)
	}
	return id
}

// event inserts an event received at the given time with an id from the same
// millisecond (ids are time-ordered, as in production).
func (e *seedEnv) event(t *testing.T, project, endpoint uuid.UUID, at time.Time, method string, contentType *string) uuid.UUID {
	t.Helper()
	id := idAt(t, at)
	if _, err := e.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params,
		  headers, content_type, body, body_size, body_encoding, correlation_id)
		VALUES ($1, $2, $3, $4, $5, '/x', '{}', '[]', $6, '\x7b7d', 2, 'utf8', 'test')`,
		id, endpoint, project, at, method, contentType); err != nil {
		t.Fatal(err)
	}
	return id
}

// idAt is a UUIDv7 whose timestamp is at's millisecond, with random low bits.
func idAt(t *testing.T, at time.Time) uuid.UUID {
	t.Helper()
	u := uuid.Must(uuid.NewV7())
	ms := at.UnixMilli()
	for i := 5; i >= 0; i-- {
		u[i] = byte(ms)
		ms >>= 8
	}
	return u
}

func strp(s string) *string { return &s }

func ids(rows []Summary) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func list(t *testing.T, e *seedEnv, owner string, project uuid.UUID, f Filter, cursor *uuid.UUID, n int32) []Summary {
	t.Helper()
	after, before := idWindow(f, cursor)
	rows, err := listRows(context.Background(), e.q, owner, project, f, after, before, n)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestListOrderingAndFilters(t *testing.T) {
	e := newSeedEnv(t)
	ep1 := e.endpoint(t, e.project, "Stripe")
	ep2 := e.endpoint(t, e.project, "GitHub")
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	a := e.event(t, e.project, ep1, base, "POST", strp("application/json"))
	b := e.event(t, e.project, ep2, base.Add(time.Minute), "PUT", strp("text/plain; charset=utf-8"))
	c := e.event(t, e.project, ep1, base.Add(2*time.Minute), "GET", nil)
	d := e.event(t, e.project, ep1, base.Add(3*time.Minute), "POST", strp("application/x-www-form-urlencoded"))
	// Noise that must never appear: another of alice's projects, and bob's project.
	other := e.Project(t, "alice", "Beta")
	e.event(t, other, e.endpoint(t, other, "Other"), base.Add(30*time.Second), "POST", strp("application/json"))
	bobs := e.Project(t, "bob", "Bob's")
	e.event(t, bobs, e.endpoint(t, bobs, "Bob"), base.Add(30*time.Second), "POST", strp("application/json"))

	at := func(d time.Duration) *time.Time { v := base.Add(d); return &v }
	cases := []struct {
		name string
		f    Filter
		want []uuid.UUID
	}{
		{"newest first by default", Filter{}, []uuid.UUID{d, c, b, a}},
		{"oldest first", Filter{Asc: true}, []uuid.UUID{a, b, c, d}},
		{"endpoint", Filter{EndpointID: &ep1}, []uuid.UUID{d, c, a}},
		{"endpoint asc", Filter{EndpointID: &ep1, Asc: true}, []uuid.UUID{a, c, d}},
		{"one method", Filter{Methods: []string{"POST"}}, []uuid.UUID{d, a}},
		{"several methods", Filter{Methods: []string{"GET", "PUT"}}, []uuid.UUID{c, b}},
		{"endpoint and method", Filter{EndpointID: &ep1, Methods: []string{"POST"}}, []uuid.UUID{d, a}},
		{"received after is inclusive", Filter{ReceivedAfter: at(time.Minute)}, []uuid.UUID{d, c, b}},
		{"received after asc", Filter{ReceivedAfter: at(time.Minute), Asc: true}, []uuid.UUID{b, c, d}},
		{"received before is exclusive", Filter{ReceivedBefore: at(2 * time.Minute)}, []uuid.UUID{b, a}},
		{"time range", Filter{ReceivedAfter: at(time.Minute), ReceivedBefore: at(3 * time.Minute)}, []uuid.UUID{c, b}},
		{"content type prefix", Filter{ContentTypePrefix: "application/"}, []uuid.UUID{d, a}},
		{"content type is case-insensitive", Filter{ContentTypePrefix: "TEXT/PLAIN"}, []uuid.UUID{b}},
		{"content type wildcards are literal", Filter{ContentTypePrefix: "%"}, []uuid.UUID{}},
		{"content type underscore is literal", Filter{ContentTypePrefix: "application_json"}, []uuid.UUID{}},
		{"no matches", Filter{Methods: []string{"DELETE"}}, []uuid.UUID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(list(t, e, "alice", e.project, tc.f, nil, 100))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("summary fields", func(t *testing.T) {
		rows := list(t, e, "alice", e.project, Filter{}, nil, 1)
		r := rows[0]
		if r.ID != d || r.EndpointID != ep1 || r.EndpointName != "Stripe" || r.Method != http.MethodPost ||
			r.Path != "/x" || r.BodySize != 2 || !r.ReceivedAt.Equal(base.Add(3*time.Minute)) ||
			r.ContentType == nil || *r.ContentType != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected summary %+v", r)
		}
		next := list(t, e, "alice", e.project, Filter{EndpointID: &ep1, Asc: true}, &a, 1)
		if len(next) != 1 || next[0].ID != c || next[0].ContentType != nil {
			t.Fatalf("event without content type: got %+v", next)
		}
	})
}

func TestListWindowAndCursorPositions(t *testing.T) {
	e := newSeedEnv(t)
	ep := e.endpoint(t, e.project, "Stripe")
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var all []uuid.UUID
	for i := range 5 {
		all = append(all, e.event(t, e.project, ep, base.Add(time.Duration(i)*time.Second), "POST", nil))
	}
	desc := slices.Clone(all)
	slices.Reverse(desc)

	if got := ids(list(t, e, "alice", e.project, Filter{}, &desc[1], 2)); !slices.Equal(got, desc[2:4]) {
		t.Fatalf("desc after cursor: got %v, want %v", got, desc[2:4])
	}
	if got := ids(list(t, e, "alice", e.project, Filter{Asc: true}, &all[1], 2)); !slices.Equal(got, all[2:4]) {
		t.Fatalf("asc after cursor: got %v, want %v", got, all[2:4])
	}
	// An asc cursor older than received_after does not widen the window.
	after := base.Add(3 * time.Second)
	if got := ids(list(t, e, "alice", e.project, Filter{Asc: true, ReceivedAfter: &after}, &all[0], 10)); !slices.Equal(got, all[3:]) {
		t.Fatalf("asc cursor before received_after: got %v, want %v", got, all[3:])
	}
	// An id generated later than its received_at (body read in between) is
	// still matched by received_after.
	late := e.event(t, e.project, ep, base.Add(10*time.Second), "POST", nil)
	if _, err := e.Admin.Exec(context.Background(),
		`UPDATE app.webhook_events SET id = $1 WHERE id = $2`, idAt(t, base.Add(12*time.Second)), late); err != nil {
		t.Fatal(err)
	}
	from := base.Add(10 * time.Second)
	if got := list(t, e, "alice", e.project, Filter{ReceivedAfter: &from}, nil, 10); len(got) != 1 {
		t.Fatalf("late id: got %d rows, want 1", len(got))
	}
}

func TestListIsOwnerScoped(t *testing.T) {
	e := newSeedEnv(t)
	ep := e.endpoint(t, e.project, "Stripe")
	e.event(t, e.project, ep, time.Now().UTC(), "POST", nil)

	if got := list(t, e, "bob", e.project, Filter{}, nil, 100); len(got) != 0 {
		t.Fatalf("bob sees %d of alice's events", len(got))
	}
	if got := list(t, e, "bob", e.project, Filter{EndpointID: &ep}, nil, 100); len(got) != 0 {
		t.Fatalf("bob sees %d events via alice's endpoint", len(got))
	}
	// Alice's endpoint filter applied to another of her projects matches nothing.
	other := e.Project(t, "alice", "Beta")
	if got := list(t, e, "alice", other, Filter{EndpointID: &ep}, nil, 100); len(got) != 0 {
		t.Fatalf("endpoint filter crossed projects: %d rows", len(got))
	}
}

func TestLastIDOfMilli(t *testing.T) {
	ms := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	edge := lastIDOfMilli(ms)
	same := idAt(t, time.UnixMilli(ms))
	next := idAt(t, time.UnixMilli(ms+1))
	if bytesGreater(same, edge) || !bytesGreater(next, edge) {
		t.Fatalf("edge %v: same %v next %v", edge, same, next)
	}
	if lastIDOfMilli(-1) != uuid.Nil {
		t.Fatal("negative millisecond must give the nil UUID")
	}
}

func TestDetailAndBodyQueriesAreOwnerScoped(t *testing.T) {
	e := newSeedEnv(t)
	ep := e.endpoint(t, e.project, "Stripe")
	id := e.event(t, e.project, ep, time.Now().UTC(), "POST", strp("application/json"))
	ctx := context.Background()

	got, err := e.q.GetEvent(ctx, store.GetEventParams{ID: pgUUID(id), ProjectID: pgUUID(e.project), OwnerID: "alice"})
	if err != nil || got.EndpointName != "Stripe" || string(got.Body) != "{}" || got.CorrelationID != "test" {
		t.Fatalf("owner detail: %+v, %v", got, err)
	}
	body, err := e.q.GetEventBody(ctx, store.GetEventBodyParams{ID: pgUUID(id), ProjectID: pgUUID(e.project), OwnerID: "alice"})
	if err != nil || string(body) != "{}" {
		t.Fatalf("owner body: %q, %v", body, err)
	}

	other := e.Project(t, "alice", "Beta")
	for name, p := range map[string]struct {
		owner   string
		project uuid.UUID
	}{"other user": {"bob", e.project}, "other project": {"alice", other}} {
		if _, err := e.q.GetEvent(ctx, store.GetEventParams{ID: pgUUID(id), ProjectID: pgUUID(p.project), OwnerID: p.owner}); err == nil {
			t.Errorf("%s: detail returned a row", name)
		}
		if _, err := e.q.GetEventBody(ctx, store.GetEventBodyParams{ID: pgUUID(id), ProjectID: pgUUID(p.project), OwnerID: p.owner}); err == nil {
			t.Errorf("%s: body returned a row", name)
		}
	}
}
