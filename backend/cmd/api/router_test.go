package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/health"
	"relaylab/internal/webui"
)

func testRouter(t *testing.T, web http.Handler) http.Handler {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := func(context.Context) error { return nil }
	return newRouter(routerDeps{cfg: cfg, logger: slog.New(slog.DiscardHandler), health: health.New(ok, ok), web: web})
}

var fakeSPA = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte("<!doctype html><title>RelayLab</title>"))
})

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	h := testRouter(t, fakeSPA)
	for _, p := range []string{"/api/v1/unknown", "/api/other", "/h/sometoken"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
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
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("/api/v1/unknown: %d %s", rec.Code, rec.Body)
	}
}
