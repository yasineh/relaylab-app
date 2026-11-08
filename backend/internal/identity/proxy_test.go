package identity

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/httpx"
)

//nolint:gosec // G101: test-only value.
const testProxySecret = "test-only-proxy-shared-secret-0123456789abcdef"

type captured struct {
	mu     sync.Mutex
	req    *http.Request
	body   []byte
	header http.Header
}

func (c *captured) get() (*http.Request, http.Header, []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.req, c.header, c.body
}

// newProxyStack mounts the proxy the way the api router does: request ID, real IP
// (trusting 10.0.0.0/8), body limit, then /api/auth/*.
func newProxyStack(t *testing.T, upstream http.Handler, timeout time.Duration, bodyLimit int) (http.Handler, *captured, *bytes.Buffer) {
	t.Helper()
	cap := &captured{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.req, cap.header, cap.body = r, r.Header.Clone(), b
		cap.mu.Unlock()
		upstream.ServeHTTP(w, r)
	}))
	t.Cleanup(up.Close)
	target, _ := url.Parse(up.URL)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := chi.NewRouter()
	r.Use(
		httpx.RequestIDMiddleware(logger),
		httpx.RealIP([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}),
		httpx.AccessLog,
		httpx.BodyLimit(bodyLimit),
	)
	r.Handle("/api/auth/*", NewAuthProxy(ProxyOptions{
		Target:      target,
		Secret:      config.NewSecret(testProxySecret),
		Timeout:     timeout,
		ForwardedTo: "https",
	}))
	return r, cap, &logs
}

func okUpstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "relaylab.session_token=abc.def; Path=/; HttpOnly; SameSite=Lax")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func TestProxyForwardsRequestAndResponse(t *testing.T) {
	h, cap, _ := newProxyStack(t, okUpstream(), 5*time.Second, 64<<10)

	body := `{"email":"a@example.test","password":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in/email?x=1", strings.NewReader(body))
	req.RemoteAddr = "10.1.2.3:5555" // trusted proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.9.9.9")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://relaylab.test")
	req.Header.Set("Cookie", "relaylab.session_token=old")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Set-Cookie"); !strings.HasPrefix(got, "relaylab.session_token=abc.def") {
		t.Errorf("Set-Cookie not returned: %q", got)
	}
	in, hdr, gotBody := cap.get()
	if in.Method != http.MethodPost || in.URL.Path != "/api/auth/sign-in/email" || in.URL.RawQuery != "x=1" {
		t.Errorf("forwarded %s %s?%s", in.Method, in.URL.Path, in.URL.RawQuery)
	}
	if string(gotBody) != body {
		t.Errorf("body = %q", gotBody)
	}
	if hdr.Get(ProxyHeader) != testProxySecret {
		t.Error("shared proxy header missing")
	}
	if got := hdr.Get("X-Forwarded-For"); got != "203.0.113.9" {
		t.Errorf("X-Forwarded-For = %q, want only the derived client IP", got)
	}
	if hdr.Get("X-Forwarded-Proto") != "https" {
		t.Errorf("X-Forwarded-Proto = %q", hdr.Get("X-Forwarded-Proto"))
	}
	if hdr.Get("Origin") != "https://relaylab.test" || hdr.Get("Cookie") != "relaylab.session_token=old" {
		t.Error("Origin and Cookie must reach the auth service unchanged")
	}
	if hdr.Get(httpx.RequestIDHeader) == "" || hdr.Get(httpx.RequestIDHeader) != rec.Header().Get(httpx.RequestIDHeader) {
		t.Error("request id not propagated")
	}
}

func TestProxyDropsSpoofedForwardingAndProxyHeaders(t *testing.T) {
	h, cap, _ := newProxyStack(t, okUpstream(), 5*time.Second, 64<<10)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/get-session", nil)
	req.RemoteAddr = "198.51.100.20:1234" // untrusted peer
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Forwarded", "for=1.2.3.4")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set(ProxyHeader, "client-guess")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "secret-hop")
	req.Header.Set("Proxy-Authorization", "Basic Zm9vOmJhcg==")
	h.ServeHTTP(httptest.NewRecorder(), req)

	_, hdr, _ := cap.get()
	if got := hdr.Get("X-Forwarded-For"); got != "198.51.100.20" {
		t.Errorf("X-Forwarded-For = %q, want the direct peer", got)
	}
	for _, k := range []string{"Forwarded", "X-Forwarded-Host", "X-Hop", "Proxy-Authorization"} {
		if v := hdr.Get(k); v != "" {
			t.Errorf("%s forwarded: %q", k, v)
		}
	}
	if hdr.Values(ProxyHeader)[0] != testProxySecret || len(hdr.Values(ProxyHeader)) != 1 {
		t.Errorf("proxy header = %v", hdr.Values(ProxyHeader))
	}
}

func TestProxyMapsRetryAfterAndStripsCORS(t *testing.T) {
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Retry-After", "297")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set(httpx.RequestIDHeader, "upstream-id")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	h, _, _ := newProxyStack(t, up, 5*time.Second, 64<<10)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in/email", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "297" {
		t.Errorf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	for k := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header leaked: %s", k)
		}
	}
	if rec.Header().Get(httpx.RequestIDHeader) == "upstream-id" {
		t.Error("upstream request id replaced ours")
	}
}

func TestProxyBodyLimit(t *testing.T) {
	h, cap, _ := newProxyStack(t, okUpstream(), 5*time.Second, 1024)
	big := strings.Repeat("x", 2048)

	// Declared length over the limit: rejected before proxying.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/sign-up/email", strings.NewReader(big)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
	if r, _, _ := cap.get(); r != nil {
		t.Error("oversized request reached the auth service")
	}

	// Chunked body (unknown length) over the limit: cut off while streaming.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-up/email", io.NopCloser(strings.NewReader(big)))
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked status = %d", rec.Code)
	}
}

func TestProxyUpstreamDown(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	target, _ := url.Parse("http://127.0.0.1:1") // nothing listens on port 1
	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware(logger))
	r.Handle("/api/auth/*", NewAuthProxy(ProxyOptions{Target: target, Secret: config.NewSecret(testProxySecret), Timeout: 2 * time.Second, ForwardedTo: "https"}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/get-session", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	var body httpx.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "auth_unavailable" {
		t.Errorf("body = %s", rec.Body)
	}
	if strings.Contains(logs.String(), "127.0.0.1:1") {
		t.Errorf("log leaks upstream address: %s", logs.String())
	}
}

func TestProxyTimeout(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})
	h, _, _ := newProxyStack(t, slow, 200*time.Millisecond, 64<<10)
	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/get-session", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d", rec.Code)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("timeout not enforced")
	}
}

func TestProxyLogsNoSecretsOrBodies(t *testing.T) {
	h, _, logs := newProxyStack(t, okUpstream(), 5*time.Second, 64<<10)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password/tok3nInPathAbCdEf123", strings.NewReader(`{"newPassword":"hunter2-hunter2"}`))
	req.Header.Set("Cookie", "relaylab.session_token=cookievalue.sig")
	h.ServeHTTP(httptest.NewRecorder(), req)
	out := logs.String()
	for _, s := range []string{testProxySecret, "hunter2", "cookievalue", "tok3nInPath", "Set-Cookie", "abc.def"} {
		if strings.Contains(out, s) {
			t.Errorf("log contains %q: %s", s, out)
		}
	}
	if !strings.Contains(out, `"route":"/api/auth/*"`) {
		t.Errorf("expected route pattern in access log: %s", out)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
