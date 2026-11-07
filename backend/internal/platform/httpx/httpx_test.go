package httpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/platform/logging"
)

// newRouter builds the standard middleware stack used by cmd/api.
func newRouter(t *testing.T, logs *bytes.Buffer, maxHeader int) chi.Router {
	t.Helper()
	logger := logging.New(logs, "api", "test", slog.LevelDebug)
	r := chi.NewRouter()
	r.Use(RequestIDMiddleware(logger), RealIP(nil), AccessLog, Recoverer, SecurityHeaders, HeaderLimit(maxHeader))
	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)
	r.Get("/api/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		logging.FromContext(r.Context()).Info("handled")
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "1"})
	})
	r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom: secret internals") })
	r.With(BodyLimit(10)).Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "too large")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return r
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) ErrorDetail {
	t.Helper()
	var b ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body is not an error envelope: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	return b.Error
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		out = append(out, m)
	}
	return out
}

func TestRequestIDGeneratedWhenAbsent(t *testing.T) {
	var logs bytes.Buffer
	rec := do(newRouter(t, &logs, 1<<20), httptest.NewRequest("GET", "/api/v1/items/1", nil))
	if id := rec.Header().Get(RequestIDHeader); len(id) != 36 {
		t.Fatalf("expected generated UUID, got %q", id)
	}
}

func TestSafeRequestIDReusedAndLogged(t *testing.T) {
	var logs bytes.Buffer
	req := httptest.NewRequest("GET", "/api/v1/items/1", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	rec := do(newRouter(t, &logs, 1<<20), req)
	if rec.Header().Get(RequestIDHeader) != "abc-123" {
		t.Fatalf("id not reused: %q", rec.Header().Get(RequestIDHeader))
	}
	lines := logLines(t, &logs)
	if len(lines) != 2 {
		t.Fatalf("expected handler + access log lines, got %d", len(lines))
	}
	for _, l := range lines {
		if l["request_id"] != "abc-123" {
			t.Fatalf("log line missing request_id: %v", l)
		}
	}
}

func TestUnsafeRequestIDReplaced(t *testing.T) {
	for _, bad := range []string{"abc\n123", strings.Repeat("a", 65), "a b", ""} {
		var logs bytes.Buffer
		req := httptest.NewRequest("GET", "/api/v1/items/1", nil)
		req.Header.Set(RequestIDHeader, bad)
		rec := do(newRouter(t, &logs, 1<<20), req)
		if got := rec.Header().Get(RequestIDHeader); got == bad || len(got) != 36 {
			t.Fatalf("unsafe id %q not replaced: %q", bad, got)
		}
	}
}

func TestAccessLogUsesRoutePatternAndOmitsSecrets(t *testing.T) {
	var logs bytes.Buffer
	req := httptest.NewRequest("GET", "/api/v1/items/item-raw-id-777?token=q-secret", nil)
	req.Header.Set("Authorization", "Bearer hdr-secret")
	req.Header.Set("Cookie", "sid=cookie-secret")
	req.Header.Set("User-Agent", "evil\n{\"level\":\"ERROR\",\"msg\":\"forged\"}")
	do(newRouter(t, &logs, 1<<20), req)

	out := logs.String()
	for _, s := range []string{"item-raw-id-777", "q-secret", "hdr-secret", "cookie-secret", "forged"} {
		if strings.Contains(out, s) {
			t.Fatalf("access log leaked %q: %s", s, out)
		}
	}
	lines := logLines(t, &logs)
	access := lines[len(lines)-1]
	if access["msg"] != "http_request" || access["route"] != "/api/v1/items/{id}" ||
		access["method"] != "GET" || access["status"] != float64(200) {
		t.Fatalf("unexpected access log: %v", access)
	}
	for _, k := range []string{"duration_ms", "bytes", "time", "level", "service"} {
		if _, ok := access[k]; !ok {
			t.Fatalf("access log missing %s: %v", k, access)
		}
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	var logs bytes.Buffer
	r := newRouter(t, &logs, 1<<20)

	rec := do(r, httptest.NewRequest("GET", "/api/v1/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	if e := decodeErr(t, rec); e.Code != "not_found" || e.RequestID == "" || e.RequestID != rec.Header().Get(RequestIDHeader) {
		t.Fatalf("bad envelope %+v", e)
	}

	rec = do(r, httptest.NewRequest("DELETE", "/api/v1/items/1", nil))
	if rec.Code != 405 || decodeErr(t, rec).Code != "method_not_allowed" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestPanicRecovered(t *testing.T) {
	var logs bytes.Buffer
	r := newRouter(t, &logs, 1<<20)
	rec := do(r, httptest.NewRequest("GET", "/panic", nil))
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if decodeErr(t, rec).Code != "internal_error" || strings.Contains(body, "goroutine") || strings.Contains(body, "secret internals") {
		t.Fatalf("panic details leaked: %s", body)
	}
	if !strings.Contains(logs.String(), "goroutine") {
		t.Fatal("stack trace not logged")
	}
	if rec := do(r, httptest.NewRequest("GET", "/api/v1/items/1", nil)); rec.Code != 200 {
		t.Fatalf("subsequent request failed: %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	var logs bytes.Buffer
	r := newRouter(t, &logs, 1<<20)
	for _, path := range []string{"/api/v1/items/1", "/api/v1/nope"} {
		rec := do(r, httptest.NewRequest("GET", path, nil))
		want := map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
			"X-Frame-Options":        "DENY",
		}
		for k, v := range want {
			if rec.Header().Get(k) != v {
				t.Fatalf("%s: %s = %q", path, k, rec.Header().Get(k))
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") ||
			!strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Fatalf("weak CSP: %q", csp)
		}
	}
}

func TestHeaderLimit(t *testing.T) {
	var logs bytes.Buffer
	r := newRouter(t, &logs, 16384)

	ok := httptest.NewRequest("GET", "/api/v1/items/1", nil)
	ok.Header.Set("X-Pad", strings.Repeat("a", 8000))
	if rec := do(r, ok); rec.Code != 200 {
		t.Fatalf("small headers rejected: %d", rec.Code)
	}

	big := httptest.NewRequest("GET", "/api/v1/items/1", nil)
	big.Header.Set("X-Pad", strings.Repeat("a", 17000))
	rec := do(r, big)
	if rec.Code != http.StatusRequestHeaderFieldsTooLarge || decodeErr(t, rec).Code != "headers_too_large" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestBodyLimit(t *testing.T) {
	var logs bytes.Buffer
	r := newRouter(t, &logs, 1<<20)
	if rec := do(r, httptest.NewRequest("POST", "/upload", strings.NewReader("small"))); rec.Code != 204 {
		t.Fatalf("small body rejected: %d", rec.Code)
	}
	rec := do(r, httptest.NewRequest("POST", "/upload", strings.NewReader(strings.Repeat("x", 50))))
	if rec.Code != 413 || decodeErr(t, rec).Code != "body_too_large" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	// Unknown length (chunked) is cut off by MaxBytesReader.
	req := httptest.NewRequest("POST", "/upload", io.MultiReader(strings.NewReader(strings.Repeat("x", 50))))
	req.ContentLength = -1
	if rec := do(r, req); rec.Code != 413 {
		t.Fatalf("chunked oversize accepted: %d", rec.Code)
	}
}

func TestRealIPTrustsOnlyConfiguredProxies(t *testing.T) {
	got := func(trusted []netip.Prefix, remote, xff string) string {
		var ip string
		h := RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			a, _ := ClientIP(r.Context())
			ip = a.String()
		}))
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return ip
	}
	proxy := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	if ip := got(nil, "203.0.113.9:1234", "1.2.3.4"); ip != "203.0.113.9" {
		t.Fatalf("untrusted peer XFF honoured: %s", ip)
	}
	if ip := got(proxy, "10.0.0.5:1234", "1.2.3.4"); ip != "1.2.3.4" {
		t.Fatalf("trusted proxy XFF ignored: %s", ip)
	}
	if ip := got(proxy, "10.0.0.5:1234", "6.6.6.6, 1.2.3.4, 10.0.0.7"); ip != "1.2.3.4" {
		t.Fatalf("spoofed leftmost XFF used: %s", ip)
	}
}

func TestGracefulShutdownCompletesInFlight(t *testing.T) {
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), h, 1<<20, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	var shutdownCalled atomic.Bool
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, srv, ln, 5*time.Second, func() { shutdownCalled.Store(true) }) }()

	type result struct {
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{body: string(b)}
	}()

	<-started
	cancel() // simulates SIGTERM via signal.NotifyContext in main

	res := <-resCh
	if res.err != nil || res.body != "done" {
		t.Fatalf("in-flight request not completed: %+v", res)
	}
	if err := <-served; err != nil {
		t.Fatalf("Serve returned %v", err)
	}
	if !shutdownCalled.Load() {
		t.Fatal("onShutdown not called")
	}
	if _, err := http.Get("http://" + ln.Addr().String()); err == nil {
		t.Fatal("server still accepting after shutdown")
	}
}
