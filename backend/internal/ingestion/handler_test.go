package ingestion

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"relaylab/internal/endpoints/token"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/redact"
)

// fakeStore records calls and can be told to fail.
type fakeStore struct {
	mu        sync.Mutex
	endpoints map[string]endpointRef // by string(hash)
	lookups   int
	saved     []event
	notified  int
	saveErr   error
	notifyErr error
	lookupErr error
}

func (f *fakeStore) Lookup(_ context.Context, hash []byte) (endpointRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.lookupErr != nil {
		return endpointRef{}, f.lookupErr
	}
	ep, ok := f.endpoints[string(hash)]
	if !ok {
		return endpointRef{}, ErrUnknownEndpoint
	}
	return ep, nil
}

func (f *fakeStore) Save(_ context.Context, ev event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, ev)
	return nil
}

func (f *fakeStore) Notify(context.Context, uuid.UUID, uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified++
	return f.notifyErr
}

// plainHasher stands in for HMAC: the "hash" is the token itself.
type plainHasher struct{}

func (plainHasher) Hash(t string) []byte { return []byte(t) }

type fixture struct {
	store   *fakeStore
	handler *Handler
	router  http.Handler
	metrics *Metrics
	logs    *bytes.Buffer
	token   string
	ep      endpointRef
}

const testMaxBody = 1024

func newFixture(t *testing.T, tune func(*Options)) *fixture {
	t.Helper()
	tok, err := token.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ep := endpointRef{ID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7()), Enabled: true}
	st := &fakeStore{endpoints: map[string]endpointRef{tok: ep}}
	reg := prometheus.NewRegistry()
	o := Options{
		Store: st, Hasher: plainHasher{}, Redactor: redact.New(nil, nil), Metrics: NewMetrics(reg),
		MaxBodyBytes: testMaxBody, MaxHeaderFields: 100, BodyTimeout: 2 * time.Second,
		EndpointPerMinute: 60, EndpointBurst: 30, IPPerMinute: 1000, IPBurst: 1000,
	}
	if tune != nil {
		tune(&o)
	}
	h := NewHandler(o)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware(logger), httpx.RealIP(nil))
	r.MethodNotAllowed(httpx.MethodNotAllowed) // as in cmd/api
	h.Routes(r)
	return &fixture{store: st, handler: h, router: r, metrics: o.Metrics, logs: logs, token: tok, ep: ep}
}

func (f *fixture) do(method, path string, body io.Reader, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "203.0.113.7:4000"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an error envelope: %q", rec.Body.String())
	}
	return body.Error.Code
}

func TestAcceptsJSONAndAcknowledgesAfterSave(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do("POST", "/h/"+f.token+"/orders/42?x=1&x=2&y=", strings.NewReader(`{"a":1}`), "Content-Type", "application/json")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var ack struct {
		OK      bool   `json:"ok"`
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil || !ack.OK {
		t.Fatalf("ack = %s", rec.Body)
	}
	if len(f.store.saved) != 1 || f.store.notified != 1 {
		t.Fatalf("saved %d notified %d", len(f.store.saved), f.store.notified)
	}
	ev := f.store.saved[0]
	if ev.ID.String() != ack.EventID || ev.Endpoint != f.ep || string(ev.Body) != `{"a":1}` ||
		ev.Captured.Path != "/orders/42" || ev.Captured.RawQuery != "x=1&x=2&y=" || string(ev.Class.JSON) != `{"a":1}` {
		t.Fatalf("event = %+v", ev)
	}
	if ev.ReceivedAt.Location() != time.UTC || ev.RequestID == "" || rec.Header().Get("X-Request-ID") != ev.RequestID {
		t.Fatalf("received %v request id %q", ev.ReceivedAt, ev.RequestID)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", rec.Header())
	}
	if *ev.Captured.SourceIPPrefix != (netipAddr("203.0.113.0")) {
		t.Fatalf("ip %v", ev.Captured.SourceIPPrefix)
	}
}

func TestAllAllowedMethodsAndHeadHasNoBody(t *testing.T) {
	f := newFixture(t, nil)
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		rec := f.do(m, "/h/"+f.token, nil)
		if rec.Code != 200 {
			t.Errorf("%s: status %d", m, rec.Code)
		}
	}
	if len(f.store.saved) != 7 {
		t.Fatalf("saved %d", len(f.store.saved))
	}
	// The real server drops HEAD bodies; check end to end.
	srv := httptest.NewServer(f.router)
	defer srv.Close()
	resp, err := http.Head(srv.URL + "/h/" + f.token)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(b) != 0 {
		t.Fatalf("HEAD %d body %q", resp.StatusCode, b)
	}
}

func TestUnsupportedMethods(t *testing.T) {
	f := newFixture(t, nil)
	for _, m := range []string{"TRACE", "CONNECT"} {
		rec := f.do(m, "/h/"+f.token, nil)
		if rec.Code != 405 || errorCode(t, rec) != "method_not_allowed" || rec.Header().Get("Allow") == "" {
			t.Errorf("%s: %d %s", m, rec.Code, rec.Body)
		}
	}
	// Methods the router does not know are rejected before ingestion runs.
	if rec := f.do("PROPFIND", "/h/"+f.token, nil); rec.Code != 405 || errorCode(t, rec) != "method_not_allowed" {
		t.Errorf("PROPFIND: %d %s", rec.Code, rec.Body)
	}
	if len(f.store.saved) != 0 || f.store.lookups != 0 {
		t.Fatal("unsupported method reached the store")
	}
}

func TestUnknownMalformedAndDisabledEndpoints(t *testing.T) {
	f := newFixture(t, nil)
	other, _ := token.Generate()
	if rec := f.do("POST", "/h/"+other, nil); rec.Code != 404 || errorCode(t, rec) != "endpoint_not_found" {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
	lookups := f.store.lookups
	for _, bad := range []string{"doesnotexist", f.token + "x", strings.Repeat("a", 65), "abc%2Fdef", "ab-cd"} {
		if rec := f.do("POST", "/h/"+bad, nil); rec.Code != 404 {
			t.Errorf("%q: %d", bad, rec.Code)
		}
	}
	if f.store.lookups != lookups {
		t.Fatal("malformed token reached the database")
	}
	// The 404 body names nothing about projects or users.
	rec := f.do("POST", "/h/"+other, nil)
	if strings.Contains(rec.Body.String(), f.ep.ProjectID.String()) {
		t.Fatal("404 leaks project information")
	}

	f.store.endpoints[f.token] = endpointRef{ID: f.ep.ID, ProjectID: f.ep.ProjectID, Enabled: false}
	if rec := f.do("POST", "/h/"+f.token, strings.NewReader("x")); rec.Code != 410 || errorCode(t, rec) != "endpoint_disabled" {
		t.Fatalf("disabled: %d %s", rec.Code, rec.Body)
	}
	if len(f.store.saved) != 0 {
		t.Fatal("rejected requests were stored")
	}
}

func TestBodyLimits(t *testing.T) {
	f := newFixture(t, nil)
	// Exactly at the limit is accepted.
	if rec := f.do("POST", "/h/"+f.token, bytes.NewReader(make([]byte, testMaxBody))); rec.Code != 200 {
		t.Fatalf("at limit: %d", rec.Code)
	}
	// Declared too large: rejected before reading or looking up.
	lookups := f.store.lookups
	req := httptest.NewRequest(http.MethodPost, "/h/"+f.token, strings.NewReader("small"))
	req.ContentLength = testMaxBody + 1
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != 413 || errorCode(t, rec) != "payload_too_large" || f.store.lookups != lookups {
		t.Fatalf("content-length: %d lookups +%d", rec.Code, f.store.lookups-lookups)
	}
	// Chunked (unknown length) over the limit stops at the limit.
	req = httptest.NewRequest(http.MethodPost, "/h/"+f.token, io.MultiReader(bytes.NewReader(make([]byte, testMaxBody)), strings.NewReader("!")))
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("chunked: %d", rec.Code)
	}
	if len(f.store.saved) != 1 {
		t.Fatalf("saved %d, want only the at-limit request", len(f.store.saved))
	}
}

func TestHeaderFieldLimit(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.MaxHeaderFields = 5 })
	var hs []string
	for i := 0; i < 5; i++ {
		hs = append(hs, "X-H", "v")
	}
	if rec := f.do("POST", "/h/"+f.token, nil, hs...); rec.Code != 431 || errorCode(t, rec) != "headers_too_large" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(f.store.saved) != 0 {
		t.Fatal("stored")
	}
}

func TestPathOrQueryTooLong(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do("GET", "/h/"+f.token+"?q="+strings.Repeat("a", maxRawQueryChars), nil)
	if rec.Code != 414 || errorCode(t, rec) != "uri_too_long" || len(f.store.saved) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestEndpointRateLimit(t *testing.T) {
	f := newFixture(t, nil)
	var limited, accepted int
	for i := 0; i < 31; i++ {
		rec := f.do("POST", "/h/"+f.token, strings.NewReader("x"))
		switch rec.Code {
		case 200:
			accepted++
		case 429:
			limited++
			if rec.Header().Get("Retry-After") == "" || errorCode(t, rec) != "rate_limited" {
				t.Fatalf("429 without Retry-After: %v", rec.Header())
			}
		default:
			t.Fatalf("status %d", rec.Code)
		}
	}
	if limited == 0 || accepted != len(f.store.saved) {
		t.Fatalf("limited %d accepted %d saved %d", limited, accepted, len(f.store.saved))
	}
}

func TestIPRateLimitRunsBeforeLookup(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.IPPerMinute, o.IPBurst = 1, 2 })
	unknown, _ := token.Generate()
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, f.do("POST", "/h/"+unknown, nil).Code)
	}
	if codes[0] != 404 || codes[1] != 404 || codes[2] != 429 || f.store.lookups != 2 {
		t.Fatalf("codes %v lookups %d", codes, f.store.lookups)
	}
}

func TestStoreFailures(t *testing.T) {
	f := newFixture(t, nil)
	f.store.saveErr = errors.New("connection refused")
	if rec := f.do("POST", "/h/"+f.token, strings.NewReader("x")); rec.Code != 503 || errorCode(t, rec) != "unavailable" {
		t.Fatalf("save failure: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(f.logs.String(), "connection refused") == false {
		t.Fatal("failure not logged for operators")
	}
	f.store.saveErr = nil
	f.store.lookupErr = errors.New("timeout")
	if rec := f.do("POST", "/h/"+f.token, strings.NewReader("x")); rec.Code != 503 {
		t.Fatalf("lookup failure: %d", rec.Code)
	}

	// A failed notification after commit still acknowledges the stored event.
	f.store.lookupErr = nil
	f.store.notifyErr = errors.New("queue full")
	if rec := f.do("POST", "/h/"+f.token, strings.NewReader("x")); rec.Code != 200 || len(f.store.saved) != 1 {
		t.Fatalf("notify failure: %d saved %d", rec.Code, len(f.store.saved))
	}
}

func TestSlowBodyTimesOut(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.BodyTimeout = 200 * time.Millisecond })
	srv := httptest.NewServer(f.router)
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "POST /h/"+f.token+" HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\nabc")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufioReader(conn), nil)
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	if resp.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(f.store.saved) != 0 {
		t.Fatal("partial event stored")
	}
}

func TestSeparateIngestHost(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.IngestHost = "in.relaylab.test" })
	req := httptest.NewRequest(http.MethodPost, "http://app.relaylab.test/h/"+f.token, nil)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("app host: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "http://IN.relaylab.test:443/h/"+f.token, nil)
	rec = httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ingest host: %d", rec.Code)
	}
}

func TestMetricsByOutcome(t *testing.T) {
	f := newFixture(t, nil)
	f.do("POST", "/h/"+f.token, strings.NewReader("ok"))
	req := httptest.NewRequest(http.MethodPost, "/h/"+f.token, nil)
	req.ContentLength = testMaxBody + 1
	f.router.ServeHTTP(httptest.NewRecorder(), req)
	if got := testutil.ToFloat64(f.metrics.requests.WithLabelValues(outcomeAccepted)); got != 1 {
		t.Fatalf("accepted = %v", got)
	}
	if got := testutil.ToFloat64(f.metrics.requests.WithLabelValues(outcomeRejectedSize)); got != 1 {
		t.Fatalf("rejected_size = %v", got)
	}
	if n := testutil.CollectAndCount(f.metrics.duration); n != 1 {
		t.Fatalf("duration series = %d", n)
	}
}

func TestLogsHoldNoSecretsAndCannotBeForged(t *testing.T) {
	f := newFixture(t, nil)
	f.do("POST", "/h/"+f.token+"/x?api_key=k123", strings.NewReader(`{"password":"hunter2"}`),
		"Authorization", "Bearer abc-secret-value",
		"Cookie", "relaylab.session_token=sess-value",
		"User-Agent", `evil\n{"level":"ERROR","msg":"forged"}`)
	logs := f.logs.String()
	for _, secret := range []string{f.token, "abc-secret-value", "sess-value", "k123", "hunter2"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains %q", secret)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not one JSON object: %q", line)
		}
		if m["msg"] == "forged" {
			t.Fatal("forged log entry")
		}
	}
	if !strings.Contains(logs, `"outcome":"accepted"`) || !strings.Contains(logs, f.ep.ID.String()) {
		t.Fatalf("ingest log line missing: %s", logs)
	}
}

func TestLimiterIsBounded(t *testing.T) {
	k := newKeyedLimiter(60, 1)
	k.max = 3
	now := time.Now()
	for _, key := range []string{"a", "b", "c", "d"} {
		k.allow(key, now)
	}
	if k.size() != 3 {
		t.Fatalf("size %d", k.size())
	}
	// "a" was evicted, so it starts with a fresh bucket.
	if ok, _ := k.allow("a", now); !ok {
		t.Fatal("evicted key not reset")
	}
	if ok, after := k.allow("a", now); ok || after < 1 {
		t.Fatalf("second request: ok=%v retry-after=%d", ok, after)
	}
}

func netipAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func bufioReader(c net.Conn) *bufio.Reader { return bufio.NewReader(c) }
