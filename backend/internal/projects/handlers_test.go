package projects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/identity"
	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/projects"
	"relaylab/internal/testutil/deletion"
	"relaylab/internal/testutil/isolation"
)

const origin = "http://relaylab.test"

type api struct {
	*env
	handler http.Handler
	alice   isolation.Requester
	bob     isolation.Requester
	anon    isolation.Requester
}

// newAPI mounts the project routes behind the production /api/v1 middleware,
// with real session validation against the database.
func newAPI(t *testing.T, limit int) *api {
	t.Helper()
	e := newEnv(t)
	appOrigin, _ := url.Parse(origin)
	v := identity.NewValidator(&config.Auth{
		CookieName:    config.SessionCookieDev,
		SessionSecret: config.NewSecret(authtest.SessionSecret),
	}, store.New(e.pool))
	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware(slog.New(slog.DiscardHandler)), httpx.BodyLimit(64<<10))
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Use(httpx.SameOrigin(appOrigin), httpx.RequireJSON, identity.RequireSession(v, true))
		v1.Route("/projects", projects.NewHandler(projects.NewService(e.pool, limit)).Routes)
	})
	a := &api{env: e, handler: r}
	a.alice = isolation.Requester{Handler: r, Origin: origin, Cookie: authtest.SessionCookie(t, e.d, "alice")}
	a.bob = isolation.Requester{Handler: r, Origin: origin, Cookie: authtest.SessionCookie(t, e.d, "bob")}
	a.anon = isolation.Requester{Handler: r, Origin: origin}
	return a
}

type projectBody struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Error       *errorBody `json:"error"`
}

type errorBody struct {
	Code   string            `json:"code"`
	Fields map[string]string `json:"fields"`
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func (a *api) create(t *testing.T, u isolation.Requester, name string) projectBody {
	t.Helper()
	rec := u.Do(http.MethodPost, "/api/v1/projects", fmt.Sprintf(`{"name":%q}`, name))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %q: %d %s", name, rec.Code, rec.Body)
	}
	return decode[projectBody](t, rec.Body.Bytes())
}

func TestHandlerCreate(t *testing.T) {
	a := newAPI(t, 20)
	rec := a.alice.Do(http.MethodPost, "/api/v1/projects", `{"name":"Stripe sandbox","description":"Test hooks"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	p := decode[projectBody](t, rec.Body.Bytes())
	if p.Name != "Stripe sandbox" || p.Description == nil || *p.Description != "Test hooks" || p.ID == "" || p.CreatedAt.IsZero() {
		t.Fatalf("body %s", rec.Body)
	}
	if rec.Header().Get("Location") != "/api/v1/projects/"+p.ID {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
	// The response has exactly the documented fields.
	keys := decode[map[string]any](t, rec.Body.Bytes())
	if len(keys) != 5 {
		t.Errorf("unexpected fields: %v", keys)
	}
	list := decode[struct{ Items []projectBody }](t, a.alice.Do(http.MethodGet, "/api/v1/projects", "").Body.Bytes())
	if len(list.Items) != 1 || list.Items[0].ID != p.ID {
		t.Fatalf("not in list: %+v", list)
	}
}

func TestHandlerCreateValidation(t *testing.T) {
	a := newAPI(t, 20)
	for _, body := range []string{`{"name":""}`, `{"name":"` + strings.Repeat("n", 81) + `"}`, `{}`, `{"name":"   "}`} {
		rec := a.alice.Do(http.MethodPost, "/api/v1/projects", body)
		p := decode[projectBody](t, rec.Body.Bytes())
		if rec.Code != http.StatusUnprocessableEntity || p.Error == nil || p.Error.Code != "validation_failed" || p.Error.Fields["name"] == "" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	for _, body := range []string{`not json`, `{"name":"x","owner_id":"bob"}`, `{"name":"x"} {"name":"y"}`, `[]`} {
		rec := a.alice.Do(http.MethodPost, "/api/v1/projects", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"invalid_json"`) {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := a.alice.Do(http.MethodPost, "/api/v1/projects", `{"name":"`+strings.Repeat("x", 70<<10)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: %d", rec.Code)
	}
}

func TestHandlerLimit(t *testing.T) {
	a := newAPI(t, 2)
	a.create(t, a.alice, "one")
	a.create(t, a.alice, "two")
	rec := a.alice.Do(http.MethodPost, "/api/v1/projects", `{"name":"three"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"project_limit_reached"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestHandlerListOnlyOwn(t *testing.T) {
	a := newAPI(t, 20)
	a.create(t, a.alice, "alice-1")
	a.create(t, a.bob, "bob-1")
	rec := a.alice.Do(http.MethodGet, "/api/v1/projects", "")
	list := decode[struct{ Items []projectBody }](t, rec.Body.Bytes())
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0].Name != "alice-1" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// A new user gets an empty list, not null.
	authtest.SeedUser(t, a.d, "carol")
	carol := isolation.Requester{Handler: a.handler, Origin: origin, Cookie: authtest.SessionCookie(t, a.d, "carol")}
	rec = carol.Do(http.MethodGet, "/api/v1/projects", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestHandlerGetAndMalformedID(t *testing.T) {
	a := newAPI(t, 20)
	p := a.create(t, a.alice, "mine")
	rec := a.alice.Do(http.MethodGet, "/api/v1/projects/"+p.ID, "")
	if rec.Code != 200 || decode[projectBody](t, rec.Body.Bytes()).Name != "mine" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000", "1%27%20OR%201=1"} {
		rec := a.alice.Do(http.MethodGet, "/api/v1/projects/"+bad, "")
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
}

func TestHandlerUpdate(t *testing.T) {
	a := newAPI(t, 20)
	p := a.create(t, a.alice, "Old")
	time.Sleep(5 * time.Millisecond)
	rec := a.alice.Do(http.MethodPatch, "/api/v1/projects/"+p.ID, `{"name":"GitHub app"}`)
	got := decode[projectBody](t, rec.Body.Bytes())
	if rec.Code != 200 || got.Name != "GitHub app" || !got.UpdatedAt.After(p.UpdatedAt) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = a.alice.Do(http.MethodPatch, "/api/v1/projects/"+p.ID, `{"description":"hooks"}`)
	if got := decode[projectBody](t, rec.Body.Bytes()); got.Description == nil || *got.Description != "hooks" || got.Name != "GitHub app" {
		t.Fatalf("set description: %s", rec.Body)
	}
	rec = a.alice.Do(http.MethodPatch, "/api/v1/projects/"+p.ID, `{"description":null}`)
	if got := decode[projectBody](t, rec.Body.Bytes()); got.Description != nil {
		t.Fatalf("clear description: %s", rec.Body)
	}
	for body, want := range map[string]int{
		`{"name":""}`:          422,
		`{"name":5}`:           422,
		`{}`:                   422,
		`{"owner_id":"bob"}`:   400,
		`{"description":true}`: 422,
	} {
		if rec := a.alice.Do(http.MethodPatch, "/api/v1/projects/"+p.ID, body); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestHandlerDeleteLeavesNoOrphans(t *testing.T) {
	a := newAPI(t, 20)
	p := a.create(t, a.alice, "doomed")
	keep := a.create(t, a.alice, "kept")
	rec := a.alice.Do(http.MethodDelete, "/api/v1/projects/"+p.ID, "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := a.alice.Do(http.MethodGet, "/api/v1/projects/"+p.ID, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
	list := decode[struct{ Items []projectBody }](t, a.alice.Do(http.MethodGet, "/api/v1/projects", "").Body.Bytes())
	if len(list.Items) != 1 || list.Items[0].ID != keep.ID {
		t.Fatalf("list after delete: %+v", list.Items)
	}
	deletion.AssertNoOrphans(t, a.admin, p.ID)
}

func TestHandlerOwnerAccountDeletionCascades(t *testing.T) {
	a := newAPI(t, 20)
	p := a.create(t, a.alice, "one")
	q := a.create(t, a.alice, "two")
	if _, err := a.admin.Exec(context.Background(), `DELETE FROM auth."user" WHERE id = 'alice'`); err != nil {
		t.Fatal(err)
	}
	deletion.AssertNoOrphans(t, a.admin, p.ID)
	deletion.AssertNoOrphans(t, a.admin, q.ID)
	// Alice's session went with her account.
	if rec := a.alice.Do(http.MethodGet, "/api/v1/projects", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user's session: %d", rec.Code)
	}
}

func TestHandlerCrossUserIsolation(t *testing.T) {
	a := newAPI(t, 20)
	p := a.create(t, a.alice, "Alice's project")
	isolation.AssertForeignNotFound(t, a.bob, p.ID, []isolation.Route{
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}"},
		{Method: http.MethodPatch, Path: "/api/v1/projects/{id}", Body: `{"name":"pwned"}`},
		{Method: http.MethodDelete, Path: "/api/v1/projects/{id}"},
	})
	got := decode[projectBody](t, a.alice.Do(http.MethodGet, "/api/v1/projects/"+p.ID, "").Body.Bytes())
	if got.Name != "Alice's project" || !got.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatalf("alice's project was changed: %+v", got)
	}
}

func TestHandlerOwnerComesFromSessionOnly(t *testing.T) {
	a := newAPI(t, 20)
	req := a.bob
	rec := req.Do(http.MethodPost, "/api/v1/projects?owner_id=alice", `{"name":"forged"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var owner string
	_ = a.admin.QueryRow(context.Background(), `SELECT owner_id FROM app.projects WHERE name = 'forged'`).Scan(&owner)
	if owner != "bob" {
		t.Fatalf("owner = %q", owner)
	}
}

func TestHandlerAuthAndCSRF(t *testing.T) {
	a := newAPI(t, 20)
	if rec := a.anon.Do(http.MethodGet, "/api/v1/projects", ""); rec.Code != 401 || !strings.Contains(rec.Body.String(), `"unauthenticated"`) {
		t.Fatalf("anonymous list: %d %s", rec.Code, rec.Body)
	}
	evil := isolation.Requester{Handler: a.handler, Origin: "https://evil.example", Cookie: a.alice.Cookie}
	if rec := evil.Do(http.MethodPost, "/api/v1/projects", `{"name":"x"}`); rec.Code != 403 {
		t.Fatalf("cross-site create: %d", rec.Code)
	}
	req := a.alice
	rec := req.Do(http.MethodGet, "/api/v1/projects", "")
	if rec.Code != 200 {
		t.Fatalf("sanity: %d", rec.Code)
	}
	var n int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM app.projects`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected requests created %d projects", n)
	}
}
