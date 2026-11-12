package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"relaylab/internal/endpoints"
	"relaylab/internal/events"
	"relaylab/internal/identity"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/redact"
	"relaylab/internal/projects"
	"relaylab/internal/webui"
)

//nolint:gosec // G101: test-only values.
const (
	testSessionSecret = "test-only-better-auth-secret-0123456789abcdef"
	testProxySecret   = "test-only-proxy-shared-secret-0123456789abcdef"
	testToken         = "OSsmUnAfkn3ueRXSDK8HJVOqRyNbSGO7"
	appOrigin         = "http://localhost:5173"
)

// sessionStore is an in-memory stand-in for the auth."session" lookup.
type sessionStore map[string]store.GetSessionByTokenRow

// sessionLookups counts session lookups across tests that inspect it.
var sessionLookups atomic.Int64

func (s sessionStore) GetSessionByToken(_ context.Context, token string) (store.GetSessionByTokenRow, error) {
	sessionLookups.Add(1)
	if r, ok := s[token]; ok {
		return r, nil
	}
	return store.GetSessionByTokenRow{}, pgx.ErrNoRows
}

// validCookie is a correctly signed session cookie for testToken (user u1).
func validCookie() *http.Cookie {
	mac := hmac.New(sha256.New, []byte(testSessionSecret))
	mac.Write([]byte(testToken))
	v := testToken + "." + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return &http.Cookie{Name: config.SessionCookieDev, Value: url.QueryEscape(v)}
}

type routerOpts struct {
	web       http.Handler
	upstream  http.Handler       // fake auth service
	ingestion *ingestion.Handler // nil: /h is not mounted
}

func testRouter(t *testing.T, web http.Handler) http.Handler {
	t.Helper()
	return testRouterWith(t, routerOpts{web: web})
}

func testRouterWith(t *testing.T, o routerOpts) http.Handler {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	authCfg, err := config.LoadAuth(func(k string) string {
		return map[string]string{"BETTER_AUTH_SECRET": testSessionSecret, "AUTH_PROXY_SHARED_SECRET": testProxySecret}[k]
	}, cfg.Env)
	if err != nil {
		t.Fatal(err)
	}
	upstream := o.upstream
	if upstream == nil {
		upstream = http.NotFoundHandler()
	}
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	target, _ := url.Parse(up.URL)

	sessions := sessionStore{testToken: {
		UserID:        "u1",
		ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		Email:         "u1@example.test",
		Name:          "User One",
		EmailVerified: true,
	}}
	ok := func(context.Context) error { return nil }
	return newRouter(routerDeps{
		cfg:       cfg,
		auth:      authCfg,
		logger:    slog.New(slog.DiscardHandler),
		health:    health.New(ok, ok),
		validator: identity.NewValidator(authCfg, sessions),
		authProxy: identity.NewAuthProxy(identity.ProxyOptions{
			Target: target, Secret: authCfg.ProxySharedSecret, Timeout: 5 * time.Second, ForwardedTo: "http",
		}),
		projects:  projects.NewHandler(projects.NewService(nil, 20)), // not called by these tests
		endpoints: endpoints.NewHandler(endpoints.NewService(nil, nil, "", 25)),
		events:    events.NewHandler(events.NewService(nil, redact.New(nil, nil)), 50, 100),
		ingestion: o.ingestion,
		web:       o.web,
	})
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an error envelope: %s", rec.Body)
	}
	if body.Error.RequestID == "" {
		t.Errorf("error envelope without request_id: %s", rec.Body)
	}
	return body.Error.Code
}

var fakeSPA = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte("<!doctype html><title>RelayLab</title>"))
})

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	h := testRouter(t, fakeSPA)
	for _, p := range []string{"/api/v1/unknown", "/api/other", "/h/sometoken"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.AddCookie(validCookie())
		h.ServeHTTP(rec, req)
		var body struct {
			Error struct{ Code, RequestID string }
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != 404 || body.Error.Code != "not_found" || strings.Contains(rec.Body.String(), "<!doctype") {
			t.Fatalf("%s: got %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestSPAFallback(t *testing.T) {
	h := testRouter(t, fakeSPA)
	for _, p := range []string{"/", "/projects/123", "/settings"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Fatalf("%s: got %d %s", p, rec.Code, rec.Body)
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: missing CSP", p)
		}
	}
	// Non-GET methods never fall back to HTML.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects", nil))
	if rec.Code == 200 {
		t.Fatalf("POST fell back to SPA")
	}
}

func TestHealthRoutesMounted(t *testing.T) {
	h := testRouter(t, nil)
	for _, p := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
}

func TestAPIBodyLimit(t *testing.T) {
	h := testRouter(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/anything", strings.NewReader(strings.Repeat("x", 70*1024)))
	req.Header.Set("Origin", appOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(validCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestEmbeddedWebAppAndAPI404(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>RelayLab</title>")}}
	h := testRouter(t, webui.Handler(fsys))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("/: %d %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	req.AddCookie(validCookie())
	h.ServeHTTP(rec, req)
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("/api/v1/unknown: %d %s", rec.Code, rec.Body)
	}
}

// TestEventPagesCSP: the explorer pages, which display untrusted webhook
// content, are served with a CSP that forbids inline and foreign scripts,
// plugins, and framing.
func TestEventPagesCSP(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>RelayLab</title>")}}
	h := testRouter(t, webui.Handler(fsys))
	for _, p := range []string{"/projects/p1/events", "/projects/p1/events/e1", "/projects/p1/events?method=POST"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %v", p, rec.Code, rec.Header())
		}
		directives := map[string]string{}
		for _, d := range strings.Split(csp, ";") {
			name, value, _ := strings.Cut(strings.TrimSpace(d), " ")
			directives[name] = value
		}
		for name, want := range map[string]string{
			"default-src": "'self'", "script-src": "'self'", "object-src": "'none'", "frame-ancestors": "'none'",
		} {
			if directives[name] != want {
				t.Errorf("%s: %s = %q, want %q (CSP %q)", p, name, directives[name], want, csp)
			}
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", p)
		}
	}
}
