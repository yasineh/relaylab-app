package demo

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"relaylab/internal/endpoints"
	"relaylab/internal/endpoints/token"
	"relaylab/internal/events"
	"relaylab/internal/identity"
	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/redact"
	"relaylab/internal/projects"
	"relaylab/internal/testutil/fixture"
	"relaylab/internal/testutil/isolation"
)

var red = redact.New(nil, nil)

// Seed fixture constraints (spec: "Seed fixture constraints").
func TestSeedFixtureIsSynthetic(t *testing.T) {
	s, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) < 6 || s.Endpoint == "" || s.Sink == "" || s.Endpoint == s.Sink {
		t.Fatalf("seed: %d events, endpoints %q/%q", len(s.Events), s.Endpoint, s.Sink)
	}
	hostRe := regexp.MustCompile(`(?i)\b(?:https?://|@)([a-z0-9.-]+\.[a-z]{2,})`)
	reserved := regexp.MustCompile(`(?i)(^|\.)(example\.(com|org|net)|[a-z0-9-]+\.(test|invalid|example))$`)
	credName := regexp.MustCompile(`(?i)(authorization|cookie|api[-_]?key|token|secret|signature|password)`)
	for i, e := range s.Events {
		text := e.Path + "?" + e.Query + "\n" + e.Body
		for _, h := range e.Headers {
			text += "\n" + h[0] + ": " + h[1]
			if credName.MatchString(h[0]) && !strings.Contains(h[1], "demo_") {
				t.Errorf("event %d: credential-like header %s is not demo_-prefixed", i, h[0])
			}
		}
		for _, m := range hostRe.FindAllStringSubmatch(text, -1) {
			if !reserved.MatchString(m[1]) {
				t.Errorf("event %d: host %q is not a reserved example domain", i, m[1])
			}
		}
		for _, kv := range strings.Split(e.Query, "&") {
			name, value, _ := strings.Cut(kv, "=")
			if credName.MatchString(name) && !strings.HasPrefix(value, "demo_") {
				t.Errorf("event %d: credential-like query %s is not demo_-prefixed", i, name)
			}
		}
	}
}

type env struct {
	*fixture.Env
	t    *testing.T
	prov *Provisioner
	h    chi.Router
	cfg  *config.Demo
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{Env: fixture.New(t, "alice"), t: t}
	e.cfg, _ = config.LoadDemo(func(string) string { return "" }, config.EnvDevelopment)
	keys, err := token.NewKeys(make([]byte, 32), make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	e.prov, err = NewProvisioner(e.Pool, keys, red, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.h = e.Router(func(v1 chi.Router) {
		v1.Use(e.prov.Middleware)
		v1.Route("/projects", projects.NewHandler(projects.NewService(e.Pool, 20).WithDemoLimit(e.cfg.MaxProjects)).Routes)
		v1.Route("/projects/{projectId}/endpoints", endpoints.NewHandler(
			endpoints.NewService(e.Pool, keys, "https://in.example.test", 25).WithDemoLimit(e.cfg.MaxEndpoints)).Routes)
		v1.Route("/projects/{projectId}/events", events.NewHandler(events.NewService(e.Pool, red), 50, 100).Routes)
	})
	return e
}

func (e *env) demoUser(id string, created time.Time) isolation.Requester {
	authtest.SeedDemoUser(e.t, e.DB, id, created)
	return e.As(e.t, e.h, id)
}

type list[T any] struct {
	Items []T `json:"items"`
}

type projectJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func decode[T any](t *testing.T, rec interface{ Bytes() []byte }) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) workspace(r isolation.Requester) projectJSON {
	e.t.Helper()
	rec := r.Do(http.MethodGet, "/api/v1/projects", "")
	if rec.Code != 200 {
		e.t.Fatalf("list projects: %d %s", rec.Code, rec.Body)
	}
	ps := decode[list[projectJSON]](e.t, rec.Body).Items
	if len(ps) != 1 || ps[0].Name != "Demo project" {
		e.t.Fatalf("workspace: %+v", ps)
	}
	return ps[0]
}

// 4.3: the first request seeds a project, two endpoints, and the events.
func TestFirstRequestSeedsWorkspace(t *testing.T) {
	e := newEnv(t)
	demo := e.demoUser("demo-a", time.Now())
	p := e.workspace(demo)

	rec := demo.Do(http.MethodGet, "/api/v1/projects/"+p.ID+"/endpoints", "")
	eps := decode[list[struct{ Name string }]](t, rec.Body).Items
	if len(eps) != 2 {
		t.Fatalf("endpoints %+v", eps)
	}
	rec = demo.Do(http.MethodGet, "/api/v1/projects/"+p.ID+"/events?limit=100", "")
	evs := decode[list[struct{ Path string }]](t, rec.Body).Items
	seed, _ := LoadSeed()
	if len(evs) != len(seed.Events) {
		t.Fatalf("%d events, want %d", len(evs), len(seed.Events))
	}
	// Seeded credentials were redacted like received ones: no demo_ secret value is stored.
	for _, leak := range []string{"demo_bearer_token_0001", "demo_api_key_0001", "demo_query_token"} {
		if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE headers::text LIKE '%'||$1||'%' OR raw_query LIKE '%'||$1||'%'`, leak); n != 0 {
			t.Errorf("%s stored unredacted", leak)
		}
	}
	if e.Count(t, `SELECT count(*) FROM app.demo_workspaces WHERE user_id = 'demo-a' AND expires_at > now() + interval '23 hours'`) != 1 {
		t.Fatal("no demo workspace row with a 24 h expiry")
	}
	// Real users get nothing seeded.
	alice := e.As(t, e.h, "alice")
	if rec := alice.Do(http.MethodGet, "/api/v1/projects", ""); len(decode[list[projectJSON]](t, rec.Body).Items) != 0 {
		t.Fatal("a real user got a seeded project")
	}
}

func TestConcurrentFirstRequestsSeedOnce(t *testing.T) {
	e := newEnv(t)
	authtest.SeedDemoUser(t, e.DB, "demo-b", time.Now())
	id := identity.Identity{UserID: "demo-b", IsDemo: true, DemoExpiresAt: time.Now().Add(24 * time.Hour)}
	var wg sync.WaitGroup
	seeded := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Fresh provisioners: no shared in-memory cache, as across API instances.
			p, _ := NewProvisioner(e.Pool, e.prov.keys, red, e.cfg)
			ok, err := p.Ensure(context.Background(), id)
			if err != nil {
				t.Error(err)
			}
			seeded <- ok
		}()
	}
	wg.Wait()
	close(seeded)
	n := 0
	for s := range seeded {
		if s {
			n++
		}
	}
	if n != 1 || e.Count(t, `SELECT count(*) FROM app.projects WHERE owner_id = 'demo-b'`) != 1 {
		t.Fatalf("seeded %d times, %d projects", n, e.Count(t, `SELECT count(*) FROM app.projects WHERE owner_id = 'demo-b'`))
	}
}

// Demo limits: one project, two endpoints.
func TestDemoLimits(t *testing.T) {
	e := newEnv(t)
	demo := e.demoUser("demo-c", time.Now())
	p := e.workspace(demo)
	rec := demo.Do(http.MethodPost, "/api/v1/projects", `{"name":"Second"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "project_limit_reached") {
		t.Fatalf("second project: %d %s", rec.Code, rec.Body)
	}
	rec = demo.Do(http.MethodPost, "/api/v1/projects/"+p.ID+"/endpoints", `{"name":"Third"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "endpoint_limit_reached") {
		t.Fatalf("third endpoint: %d %s", rec.Code, rec.Body)
	}
	// A real user's limits are unchanged.
	alice := e.As(t, e.h, "alice")
	for _, name := range []string{"One", "Two"} {
		if rec := alice.Do(http.MethodPost, "/api/v1/projects", `{"name":"`+name+`"}`); rec.Code != http.StatusCreated {
			t.Fatalf("alice project %s: %d", name, rec.Code)
		}
	}
}

// 4.7: demo users only see their own workspace.
func TestCrossDemoIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.demoUser("demo-d", time.Now())
	b := e.demoUser("demo-e", time.Now())
	pa := e.workspace(a)
	pb := e.workspace(b)
	if pa.ID == pb.ID {
		t.Fatal("two demo users share a project")
	}
	rec := a.Do(http.MethodGet, "/api/v1/projects/"+pb.ID+"/events?limit=1", "")
	ev := decode[list[struct{ ID string }]](t, b.Do(http.MethodGet, "/api/v1/projects/"+pb.ID+"/events?limit=1", "").Body).Items
	if len(ev) != 1 {
		t.Fatal("b has no events")
	}
	for _, path := range []string{
		"/api/v1/projects/" + pb.ID,
		"/api/v1/projects/" + pb.ID + "/events",
		"/api/v1/projects/" + pb.ID + "/events/" + ev[0].ID,
		"/api/v1/projects/" + pb.ID + "/endpoints",
	} {
		rec = a.Do(http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), pb.ID) && path != "/api/v1/projects/"+pb.ID {
			t.Errorf("%s leaks b's data", path)
		}
	}
}

// An expired demo session is refused even before the purge deletes the user.
func TestExpiredDemoSessionRefused(t *testing.T) {
	e := newEnv(t)
	old := e.demoUser("demo-f", time.Now().Add(-25*time.Hour))
	rec := old.Do(http.MethodGet, "/api/v1/projects", "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "session_expired") {
		t.Fatalf("expired demo: %d %s", rec.Code, rec.Body)
	}
	if e.Count(t, `SELECT count(*) FROM app.projects WHERE owner_id = 'demo-f'`) != 0 {
		t.Fatal("expired demo user got a workspace")
	}
}

// 4.6: deleting the demo user (what the auth service's purge does, as
// auth_runtime) removes every app row by cascade; real users keep theirs.
func TestDeletingDemoUserRemovesWorkspace(t *testing.T) {
	e := newEnv(t)
	demo := e.demoUser("demo-g", time.Now())
	p := e.workspace(demo)
	alicesProject := e.Project(t, "alice", "Real")
	ctx := context.Background()
	if _, err := e.Admin.Exec(ctx, `INSERT INTO app.audit_log (id, project_id, actor_user_id, action, resource_type)
		VALUES (gen_random_uuid(), $1, 'demo-g', 'replay.created', 'replay')`, p.ID); err != nil {
		t.Fatal(err)
	}
	authConn, err := pgxConnect(ctx, e.DB.AuthRuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = authConn.Close(ctx) }()
	if _, err := authConn.Exec(ctx, `DELETE FROM auth."user" WHERE id = 'demo-g' AND "isAnonymous"`); err != nil {
		t.Fatalf("delete as auth_runtime: %v", err)
	}
	for _, table := range []string{"endpoints", "webhook_events", "audit_log"} {
		if n := e.Count(t, `SELECT count(*) FROM app.`+table+` WHERE project_id::text = $1`, p.ID); n != 0 {
			t.Errorf("%s rows remain: %d", table, n)
		}
	}
	if n := e.Count(t, `SELECT count(*) FROM app.projects WHERE owner_id = 'demo-g'`); n != 0 {
		t.Errorf("%d demo projects remain", n)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.demo_workspaces WHERE user_id = 'demo-g'`); n != 0 {
		t.Error("demo workspace row remains")
	}
	if n := e.Count(t, `SELECT count(*) FROM auth."session" WHERE "userId" = 'demo-g'`); n != 0 {
		t.Error("demo sessions remain")
	}
	if e.Count(t, `SELECT count(*) FROM app.projects WHERE id = $1`, alicesProject) != 1 {
		t.Fatal("a real user's project was deleted")
	}
	if rec := demo.Do(http.MethodGet, "/api/v1/projects", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old demo cookie: %d", rec.Code)
	}
}

func pgxConnect(ctx context.Context, url string) (*pgx.Conn, error) { return pgx.Connect(ctx, url) }
