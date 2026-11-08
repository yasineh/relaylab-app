package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"relaylab/internal/identity"
)

// Handler tests for the add-user-auth spec scenarios at the router level.

func TestMeRequiresSession(t *testing.T) {
	h := testRouter(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "unauthenticated" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestUnknownV1PathRequiresSession(t *testing.T) {
	h := testRouter(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d: route existence observable without a session", rec.Code)
	}
}

func TestMeWithSessionIgnoresForgedUserHeader(t *testing.T) {
	h := testRouter(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me?user_id=u2", nil)
	req.AddCookie(validCookie())
	req.Header.Set("X-User-Id", "u2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	var me struct {
		ID, Email, Name string
		EmailVerified   bool `json:"email_verified"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if me.ID != "u1" || me.Email != "u1@example.test" || me.Name != "User One" || !me.EmailVerified {
		t.Fatalf("me = %+v", me)
	}
}

func TestPublicConfigWithoutSession(t *testing.T) {
	h := testRouter(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 2 || body["signup_enabled"] != true || body["email_verification_required"] != true {
		t.Fatalf("config = %v", body)
	}
	// Only GET is public; anything else on the path still needs a session.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader("{}"))
	req.Header.Set("Origin", appOrigin)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/config: %d", rec.Code)
	}
}

func TestCrossSiteFormPostRejected(t *testing.T) {
	h := testRouter(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader("name=pwned"))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(validCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "origin_rejected" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestSameOriginWrongContentType(t *testing.T) {
	h := testRouter(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Origin", appOrigin)
	req.Header.Set("Content-Type", "text/plain")
	req.AddCookie(validCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType || errorCode(t, rec) != "unsupported_media_type" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestNoCORSHeadersOnAPI(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
	})
	h := testRouterWith(t, routerOpts{upstream: upstream})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/config"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodOptions, "/api/v1/me"},
		{http.MethodGet, "/api/auth/get-session"},
		{http.MethodOptions, "/api/auth/sign-in/email"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		for k := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s %s: CORS header %s", tc.method, tc.path, k)
			}
		}
	}
}

func TestAuthRequestsAreProxied(t *testing.T) {
	var hits atomic.Int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get(identity.ProxyHeader) != testProxySecret || r.URL.Path != "/api/auth/sign-in/email" || string(b) != `{"email":"a@b.test"}` {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.Header().Set("Set-Cookie", "relaylab.session_token=x.y; Path=/; HttpOnly; SameSite=Lax")
		w.WriteHeader(http.StatusOK)
	})
	h := testRouterWith(t, routerOpts{upstream: upstream})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in/email", strings.NewReader(`{"email":"a@b.test"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || hits.Load() != 1 || rec.Header().Get("Set-Cookie") == "" {
		t.Fatalf("got %d hits=%d headers=%v", rec.Code, hits.Load(), rec.Header())
	}
}

func TestPublicRoutesIgnoreSessionCookie(t *testing.T) {
	h := testRouter(t, nil)
	// Health and future ingestion paths work with a garbage cookie and without any cookie.
	for _, p := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Cookie", "relaylab.session_token=garbage")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s with cookie: %d", p, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/h/abc", nil)
	req.Header.Set("Cookie", "relaylab.session_token=garbage")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("/h/* must not depend on the session cookie")
	}
}
