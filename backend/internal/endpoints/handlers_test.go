package endpoints_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/endpoints"
	"relaylab/internal/testutil/deletion"
	"relaylab/internal/testutil/fixture"
	"relaylab/internal/testutil/isolation"
)

type api struct {
	*fixture.Env
	h       chi.Router
	alice   isolation.Requester
	bob     isolation.Requester
	project uuid.UUID
}

func newAPI(t *testing.T, limit int) *api {
	t.Helper()
	e := fixture.New(t, "alice", "bob")
	svc := endpoints.NewService(e.Pool, testKeys(t), ingest, limit)
	h := e.Router(func(v1 chi.Router) {
		v1.Route("/projects/{projectId}/endpoints", endpoints.NewHandler(svc).Routes)
	})
	return &api{Env: e, h: h, alice: e.As(t, h, "alice"), bob: e.As(t, h, "bob"), project: e.Project(t, "alice", "Alice's")}
}

func (a *api) base(p uuid.UUID) string { return "/api/v1/projects/" + p.String() + "/endpoints" }

type epBody struct {
	ID             string  `json:"id"`
	ProjectID      string  `json:"project_id"`
	Name           string  `json:"name"`
	Enabled        bool    `json:"enabled"`
	URL            *string `json:"url"`
	LastReceivedAt *string `json:"last_received_at"`
	Error          *struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func (a *api) create(t *testing.T, name string) epBody {
	t.Helper()
	rec := a.alice.Do(http.MethodPost, a.base(a.project), fmt.Sprintf(`{"name":%q}`, name))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	return decode[epBody](t, rec.Body.Bytes())
}

func TestHandlerCreate201(t *testing.T) {
	a := newAPI(t, 25)
	rec := a.alice.Do(http.MethodPost, a.base(a.project), `{"name":"Stripe staging"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	b := decode[epBody](t, rec.Body.Bytes())
	if b.Name != "Stripe staging" || !b.Enabled || b.URL == nil || !urlPattern.MatchString(*b.URL) || b.ProjectID != a.project.String() {
		t.Fatalf("%s", rec.Body)
	}
	if rec.Header().Get("Location") != a.base(a.project)+"/"+b.ID || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", rec.Header())
	}
	// Exactly the documented fields: no hash, ciphertext, or key material.
	fields := decode[map[string]any](t, rec.Body.Bytes())
	for _, k := range []string{"token_hash", "token_ciphertext", "token_key_version", "token", "token_rotated_at"} {
		if _, ok := fields[k]; ok {
			t.Errorf("response exposes %s", k)
		}
	}
	if len(fields) != 8 {
		t.Errorf("fields: %v", fields)
	}
}

func TestHandlerListAndGet200(t *testing.T) {
	a := newAPI(t, 25)
	rec := a.alice.Do(http.MethodGet, a.base(a.project), "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	one := a.create(t, "one")
	two := a.create(t, "two")
	list := decode[struct{ Items []epBody }](t, a.alice.Do(http.MethodGet, a.base(a.project), "").Body.Bytes())
	if len(list.Items) != 2 || list.Items[0].ID != two.ID || list.Items[1].ID != one.ID || list.Items[0].URL == nil {
		t.Fatalf("list: %+v", list.Items)
	}
	got := decode[epBody](t, a.alice.Do(http.MethodGet, a.base(a.project)+"/"+one.ID, "").Body.Bytes())
	if got.Name != "one" || *got.URL != *one.URL {
		t.Fatalf("get: %+v", got)
	}
}

func TestHandlerValidation422AndBadJSON400(t *testing.T) {
	a := newAPI(t, 25)
	for _, body := range []string{`{"name":""}`, `{"name":"   "}`, `{}`, `{"name":"` + strings.Repeat("n", 101) + `"}`, `{"name":"a\u0007b"}`} {
		rec := a.alice.Do(http.MethodPost, a.base(a.project), body)
		b := decode[epBody](t, rec.Body.Bytes())
		if rec.Code != 422 || b.Error == nil || b.Error.Code != "validation_failed" || b.Error.Fields["name"] == "" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := a.alice.Do(http.MethodPost, a.base(a.project), `{"name":"x","enabled":false}`); rec.Code != 400 {
		t.Errorf("unknown field from the owner: %d", rec.Code)
	}
	ep := a.create(t, "x")
	for body, want := range map[string]int{`{"name":""}`: 422, `{"enabled":"no"}`: 422, `{}`: 422, `{"url":"x"}`: 400} {
		if rec := a.alice.Do(http.MethodPatch, a.base(a.project)+"/"+ep.ID, body); rec.Code != want {
			t.Errorf("patch %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if n := a.Count(t, `SELECT count(*) FROM app.endpoints`); n != 1 {
		t.Fatalf("%d endpoints", n)
	}
}

func TestHandlerLimit409(t *testing.T) {
	a := newAPI(t, 2)
	a.create(t, "one")
	a.create(t, "two")
	rec := a.alice.Do(http.MethodPost, a.base(a.project), `{"name":"three"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"endpoint_limit_reached"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestHandlerRenameEnableRotate200(t *testing.T) {
	a := newAPI(t, 25)
	ep := a.create(t, "old")
	path := a.base(a.project) + "/" + ep.ID

	renamed := decode[epBody](t, a.alice.Do(http.MethodPatch, path, `{"name":"GitHub"}`).Body.Bytes())
	if renamed.Name != "GitHub" || *renamed.URL != *ep.URL {
		t.Fatalf("rename: %+v", renamed)
	}
	disabled := decode[epBody](t, a.alice.Do(http.MethodPatch, path, `{"enabled":false}`).Body.Bytes())
	if disabled.Enabled || *disabled.URL != *ep.URL {
		t.Fatalf("disable: %+v", disabled)
	}
	rec := a.alice.Do(http.MethodPost, path+"/rotate", "")
	rotated := decode[epBody](t, rec.Body.Bytes())
	if rec.Code != 200 || rotated.URL == nil || *rotated.URL == *ep.URL || !urlPattern.MatchString(*rotated.URL) || rotated.Enabled {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
}

func TestHandlerDelete204AndCascade(t *testing.T) {
	a := newAPI(t, 25)
	ep := a.create(t, "doomed")
	rec := a.alice.Do(http.MethodDelete, a.base(a.project)+"/"+ep.ID, "")
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := a.alice.Do(http.MethodGet, a.base(a.project)+"/"+ep.ID, ""); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
	// Deleting the project removes its endpoints and audit trail.
	a.create(t, "survivor")
	if _, err := a.Admin.Exec(context.Background(), `DELETE FROM app.projects WHERE id = $1`, a.project); err != nil {
		t.Fatal(err)
	}
	deletion.AssertNoOrphans(t, a.Admin, a.project)
}

func TestHandler404ForUnknownAndMalformedIDs(t *testing.T) {
	a := newAPI(t, 25)
	for _, path := range []string{
		a.base(a.project) + "/" + uuid.NewString(),
		a.base(a.project) + "/not-a-uuid",
		"/api/v1/projects/not-a-uuid/endpoints",
		a.base(uuid.Must(uuid.NewV7())),
	} {
		rec := a.alice.Do(http.MethodGet, path, "")
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestHandler401And403(t *testing.T) {
	a := newAPI(t, 25)
	anon := isolation.Requester{Handler: a.h, Origin: fixture.Origin}
	if rec := anon.Do(http.MethodGet, a.base(a.project), ""); rec.Code != 401 || !strings.Contains(rec.Body.String(), `"unauthenticated"`) {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}
	evil := isolation.Requester{Handler: a.h, Origin: "https://evil.example", Cookie: a.alice.Cookie}
	if rec := evil.Do(http.MethodPost, a.base(a.project), `{"name":"x"}`); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"origin_rejected"`) {
		t.Fatalf("cross-origin: %d %s", rec.Code, rec.Body)
	}
	if n := a.Count(t, `SELECT count(*) FROM app.endpoints`); n != 0 {
		t.Fatal("rejected request created an endpoint")
	}
}

// --- 3.3: isolation with real sessions ---

func TestIsolationAnotherUsersEndpoints(t *testing.T) {
	a := newAPI(t, 25)
	ep := a.create(t, "Alice's endpoint")
	// Every per-endpoint operation as bob: same 404 as a nonexistent endpoint.
	isolation.AssertForeignNotFound(t, a.bob, ep.ID, []isolation.Route{
		{Method: http.MethodGet, Path: a.base(a.project) + "/{id}"},
		{Method: http.MethodPatch, Path: a.base(a.project) + "/{id}", Body: `{"name":"pwned"}`},
		{Method: http.MethodPatch, Path: a.base(a.project) + "/{id}", Body: `{"enabled":false}`},
		{Method: http.MethodPost, Path: a.base(a.project) + "/{id}/rotate"},
		{Method: http.MethodDelete, Path: a.base(a.project) + "/{id}"},
	})
	// Project-level routes as bob: same 404 as a nonexistent project.
	isolation.AssertForeignNotFound(t, a.bob, a.project.String(), []isolation.Route{
		{Method: http.MethodGet, Path: "/api/v1/projects/{id}/endpoints"},
		{Method: http.MethodPost, Path: "/api/v1/projects/{id}/endpoints", Body: `{"name":"intruder"}`},
	})
	got := decode[epBody](t, a.alice.Do(http.MethodGet, a.base(a.project)+"/"+ep.ID, "").Body.Bytes())
	if got.Name != "Alice's endpoint" || !got.Enabled || *got.URL != *ep.URL {
		t.Fatalf("alice's endpoint changed: %+v", got)
	}
	if n := a.Count(t, `SELECT count(*) FROM app.endpoints WHERE project_id = $1`, a.project); n != 1 {
		t.Fatalf("bob created endpoints in alice's project: %d", n)
	}
}

func TestIsolationClientSuppliedOwnerIgnored(t *testing.T) {
	a := newAPI(t, 25)
	rec := a.bob.Do(http.MethodPost, a.base(a.project), `{"name":"x","owner_id":"alice"}`)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if n := a.Count(t, `SELECT count(*) FROM app.endpoints`); n != 0 {
		t.Fatal("endpoint created")
	}
}

func TestIsolationEndpointFromAnotherProject(t *testing.T) {
	a := newAPI(t, 25)
	ep := a.create(t, "in X")
	y := a.Project(t, "alice", "Project Y")
	for _, rt := range []isolation.Route{
		{Method: http.MethodGet, Path: a.base(y) + "/" + ep.ID},
		{Method: http.MethodPatch, Path: a.base(y) + "/" + ep.ID, Body: `{"name":"moved"}`},
		{Method: http.MethodPost, Path: a.base(y) + "/" + ep.ID + "/rotate"},
		{Method: http.MethodDelete, Path: a.base(y) + "/" + ep.ID},
	} {
		if rec := a.alice.Do(rt.Method, rt.Path, rt.Body); rec.Code != 404 {
			t.Errorf("%s %s: %d", rt.Method, rt.Path, rec.Code)
		}
	}
}

func TestIsolationExpiredAndRevokedSessions(t *testing.T) {
	a := newAPI(t, 25)
	ep := a.create(t, "x")
	ctx := context.Background()

	expired := a.As(t, a.h, "alice")
	revoked := a.As(t, a.h, "alice")
	tokenOfCookie := func(c string) string {
		// relaylab.session_token=<token>.<url-escaped signature>; the token is hex.
		return strings.SplitN(strings.SplitN(c, "=", 2)[1], ".", 2)[0]
	}
	if _, err := a.Admin.Exec(ctx, `UPDATE auth."session" SET "expiresAt" = now() - interval '1 second' WHERE token = $1`, tokenOfCookie(expired.Cookie)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Admin.Exec(ctx, `DELETE FROM auth."session" WHERE token = $1`, tokenOfCookie(revoked.Cookie)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		u    isolation.Requester
		code string
	}{"expired": {expired, "session_expired"}, "revoked": {revoked, "unauthenticated"}} {
		for _, rt := range []isolation.Route{
			{Method: http.MethodGet, Path: a.base(a.project)},
			{Method: http.MethodPatch, Path: a.base(a.project) + "/" + ep.ID, Body: `{"name":"changed"}`},
			{Method: http.MethodPost, Path: a.base(a.project) + "/" + ep.ID + "/rotate"},
			{Method: http.MethodDelete, Path: a.base(a.project) + "/" + ep.ID},
		} {
			rec := tc.u.Do(rt.Method, rt.Path, rt.Body)
			if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
				t.Errorf("%s %s %s: %d %s", name, rt.Method, rt.Path, rec.Code, rec.Body)
			}
		}
	}
	got := decode[epBody](t, a.alice.Do(http.MethodGet, a.base(a.project)+"/"+ep.ID, "").Body.Bytes())
	if got.Name != "x" || *got.URL != *ep.URL {
		t.Fatalf("endpoint changed by rejected sessions: %+v", got)
	}
}
