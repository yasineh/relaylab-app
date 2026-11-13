package ingestion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"relaylab/internal/endpoints"
	"relaylab/internal/endpoints/token"
	"relaylab/internal/identity/authtest"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/redact"
	"relaylab/internal/testutil/fixture"
)

type env struct {
	*fixture.Env
	svc     *endpoints.Service
	router  http.Handler
	logs    *bytes.Buffer
	project uuid.UUID
	perfURL string
}

func newEnv(t *testing.T, store ingestion.Store) *env {
	t.Helper()
	return newEnvWith(t, store, nil)
}

func newEnvWith(t *testing.T, store ingestion.Store, tune func(*ingestion.Options)) *env {
	t.Helper()
	fx := fixture.New(t, "alice")
	keys, err := token.NewKeys(bytes.Repeat([]byte{0xA1}, 32), bytes.Repeat([]byte{0xB2}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		store = ingestion.NewPGStore(fx.Pool)
	}
	limits := config.DefaultLimits()
	o := ingestion.Options{
		Store: store, Hasher: keys, Redactor: redact.New(nil, nil), Metrics: ingestion.NewMetrics(prometheus.NewRegistry()),
		MaxBodyBytes: limits.IngestMaxBodyBytes, MaxHeaderFields: limits.IngestMaxHeaderFields,
		BodyTimeout:       time.Duration(limits.IngestBodyTimeoutSeconds) * time.Second,
		EndpointPerMinute: limits.IngestRatePerEndpointPerMinute, EndpointBurst: limits.IngestRateEndpointBurst,
		IPPerMinute: limits.IngestRatePerIPPerMinute, IPBurst: limits.IngestRateIPBurst,
	}
	if tune != nil {
		tune(&o)
	}
	h := ingestion.NewHandler(o)
	logs := &bytes.Buffer{}
	r := chi.NewRouter()
	// The production middleware that wraps ingestion in cmd/api.
	r.Use(httpx.RequestIDMiddleware(slog.New(slog.NewJSONHandler(logs, nil))), httpx.RealIP(nil),
		httpx.AccessLog, httpx.Recoverer, httpx.SecurityHeaders, httpx.HeaderLimit(limits.HTTPMaxHeaderBytes))
	h.Routes(r)
	return &env{
		Env:     fx,
		svc:     endpoints.NewService(fx.Pool, keys, "http://relaylab.test", limits.EndpointsPerProject),
		router:  r,
		logs:    logs,
		project: fx.Project(t, "alice", "Webhooks"),
	}
}

// endpoint creates an endpoint and returns it with its token.
func (e *env) endpoint(t *testing.T) (endpoints.Endpoint, string) {
	t.Helper()
	ep, err := e.svc.Create(context.Background(), "alice", e.project, "Stripe staging")
	if err != nil {
		t.Fatal(err)
	}
	return ep, (*ep.URL)[strings.LastIndex(*ep.URL, "/")+1:]
}

func (e *env) send(method, target string, body []byte, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.23:5000"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

type storedEvent struct {
	EndpointID, ProjectID       uuid.UUID
	Method, Path, RawQuery      string
	Params                      map[string][]string
	Headers                     [][2]string
	RedactedCount               int
	ContentType                 *string
	Body                        []byte
	BodySize                    int
	Encoding                    string
	BodyJSON                    []byte
	JSONInvalid                 bool
	SourceIP, UserAgent, CorrID *string
	ReceivedAt                  time.Time
}

func (e *env) event(t *testing.T, id string) storedEvent {
	t.Helper()
	var s storedEvent
	var params, headers []byte
	err := e.Admin.QueryRow(context.Background(), `
		SELECT endpoint_id, project_id, method, path, raw_query, query_params, headers, redacted_header_count,
		       content_type, body, body_size, body_encoding, body_json, json_invalid,
		       host(source_ip_prefix), user_agent, correlation_id, received_at
		FROM app.webhook_events WHERE id = $1`, id).Scan(
		&s.EndpointID, &s.ProjectID, &s.Method, &s.Path, &s.RawQuery, &params, &headers, &s.RedactedCount,
		&s.ContentType, &s.Body, &s.BodySize, &s.Encoding, &s.BodyJSON, &s.JSONInvalid,
		&s.SourceIP, &s.UserAgent, &s.CorrID, &s.ReceivedAt)
	if err != nil {
		t.Fatalf("load event %s: %v", id, err)
	}
	_ = json.Unmarshal(params, &s.Params)
	_ = json.Unmarshal(headers, &s.Headers)
	return s
}

func eventID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var ack struct {
		OK      bool   `json:"ok"`
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil || !ack.OK || ack.EventID == "" {
		t.Fatalf("ack = %s", rec.Body)
	}
	return ack.EventID
}

func TestCaptureScenariosArePersistedFaithfully(t *testing.T) {
	e := newEnv(t, nil)
	ep, tok := e.endpoint(t)
	base := "/h/" + tok

	// JSON POST.
	s := e.event(t, eventID(t, e.send("POST", base, []byte(`{"a":1}`), "Content-Type", "application/json")))
	if s.Method != http.MethodPost || string(s.Body) != `{"a":1}` || *s.ContentType != "application/json" ||
		string(s.BodyJSON) != `{"a": 1}` || s.Encoding != "utf8" || s.EndpointID != ep.ID || s.ProjectID != e.project {
		t.Fatalf("json event = %+v (body_json %s)", s, s.BodyJSON)
	}
	if *s.SourceIP != "198.51.100.0" || *s.CorrID == "" || s.ReceivedAt.IsZero() {
		t.Fatalf("metadata = ip %v corr %v", *s.SourceIP, *s.CorrID)
	}

	// Subpath and repeated query parameters.
	s = e.event(t, eventID(t, e.send("GET", base+"/orders/42?x=1&x=2&y=", nil)))
	if s.Path != "/orders/42" || s.RawQuery != "x=1&x=2&y=" || len(s.Params["x"]) != 2 || s.Params["y"][0] != "" {
		t.Fatalf("subpath event = %+v", s)
	}

	// Form, binary, invalid JSON, empty, and unknown content type.
	s = e.event(t, eventID(t, e.send("POST", base, []byte("a=1&b=two"), "Content-Type", "application/x-www-form-urlencoded")))
	if string(s.Body) != "a=1&b=two" || s.BodyJSON != nil {
		t.Fatalf("form = %+v", s)
	}
	s = e.event(t, eventID(t, e.send("POST", base, []byte{0xff, 0xfe, 0x00}, "Content-Type", "application/octet-stream")))
	if !bytes.Equal(s.Body, []byte{0xff, 0xfe, 0x00}) || s.Encoding != "binary" || s.BodySize != 3 {
		t.Fatalf("binary = %+v", s)
	}
	s = e.event(t, eventID(t, e.send("POST", base, []byte(`{"a":`), "Content-Type", "application/json")))
	if !s.JSONInvalid || s.BodyJSON != nil || string(s.Body) != `{"a":` {
		t.Fatalf("invalid json = %+v", s)
	}
	s = e.event(t, eventID(t, e.send("POST", base, nil)))
	if s.BodySize != 0 || s.Body == nil {
		t.Fatalf("empty = %+v", s)
	}
	s = e.event(t, eventID(t, e.send("POST", base, []byte(`{"a":1}`), "Content-Type", "application/x-unknown; charset=bogus")))
	if *s.ContentType != "application/x-unknown; charset=bogus" || s.BodyJSON != nil {
		t.Fatalf("unknown type = %+v", s)
	}
	// A body containing a field named password is stored unmodified.
	s = e.event(t, eventID(t, e.send("POST", base, []byte(`{"password":"p"}`), "Content-Type", "application/json")))
	if string(s.Body) != `{"password":"p"}` {
		t.Fatalf("body rewritten: %s", s.Body)
	}

	// The endpoint's last-received time moved forward.
	got, err := e.svc.Get(context.Background(), "alice", e.project, ep.ID)
	if err != nil || got.LastReceivedAt == nil {
		t.Fatalf("last_received_at not set: %+v %v", got, err)
	}
}

func TestSQLInjectionPathIsStoredLiterally(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.endpoint(t)
	s := e.event(t, eventID(t, e.send("GET", "/h/"+tok+"/'%3B%20DROP%20TABLE%20webhook_events%3B--?q=1'%20OR%20'1'='1", nil)))
	if s.Path != "/'; DROP TABLE webhook_events;--" {
		t.Fatalf("path = %q", s.Path)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events`); n != 1 {
		t.Fatalf("events = %d", n)
	}
}

func TestSecretsNeverReachTheRowOrLogs(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.endpoint(t)
	secrets := []string{"sk_live_abc123", "sid=xyz", "deadbeef", "sha256=abcd", "k123", "tok999", "shpfy-hmac", "p-77"}
	id := eventID(t, e.send("POST", "/h/"+tok+"?api_key=k123&access_token=tok999&page=2",
		[]byte(`{"event":"charge.succeeded"}`),
		"Content-Type", "application/json",
		"Authorization", "Bearer sk_live_abc123",
		"Cookie", "sid=xyz",
		"Stripe-Signature", "t=1,v1=deadbeef",
		"X-Hub-Signature-256", "sha256=abcd",
		"X-Shopify-Hmac-Sha256", "shpfy-hmac",
		"X-GitHub-Event", "push",
	))

	var row string
	if err := e.Admin.QueryRow(context.Background(), `SELECT t::text FROM app.webhook_events t WHERE id = $1`, id).Scan(&row); err != nil {
		t.Fatal(err)
	}
	logs := e.logs.String()
	for _, s := range secrets {
		if strings.Contains(row, s) {
			t.Errorf("row contains %q", s)
		}
		if strings.Contains(logs, s) {
			t.Errorf("logs contain %q", s)
		}
	}
	if strings.Contains(logs, tok) {
		t.Error("logs contain the endpoint token")
	}

	ev := e.event(t, id)
	want := map[string]string{
		"Authorization": redact.Marker, "Cookie": redact.Marker, "Stripe-Signature": redact.Marker,
		"X-Hub-Signature-256": redact.Marker, "X-Shopify-Hmac-Sha256": redact.Marker,
		"X-Github-Event": "push", "Content-Type": "application/json",
	}
	got := map[string]string{}
	for _, h := range ev.Headers {
		got[h[0]] = h[1]
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("header %s = %q, want %q", name, got[name], v)
		}
	}
	if ev.RedactedCount != 5 {
		t.Errorf("redacted count = %d", ev.RedactedCount)
	}
	if ev.RawQuery != "api_key=%5BREDACTED%5D&access_token=%5BREDACTED%5D&page=2" ||
		ev.Params["api_key"][0] != redact.Marker || ev.Params["page"][0] != "2" {
		t.Errorf("query = %q %v", ev.RawQuery, ev.Params)
	}
}

func TestEndpointChangesApplyToTheNextRequest(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	ep, tok := e.endpoint(t)
	eventID(t, e.send("POST", "/h/"+tok, []byte("before")))

	// Disable: 410 immediately, re-enable: accepted again.
	off, on := false, true
	if _, err := e.svc.Update(ctx, "alice", e.project, ep.ID, nil, &off); err != nil {
		t.Fatal(err)
	}
	if rec := e.send("POST", "/h/"+tok, []byte("x")); rec.Code != http.StatusGone {
		t.Fatalf("disabled: %d", rec.Code)
	}
	if _, err := e.svc.Update(ctx, "alice", e.project, ep.ID, nil, &on); err != nil {
		t.Fatal(err)
	}
	eventID(t, e.send("POST", "/h/"+tok, []byte("x")))

	// Rotate: the old URL is unknown at once, the new one works, and events stay.
	rotated, err := e.svc.Rotate(ctx, "alice", e.project, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.send("POST", "/h/"+tok, []byte("x")); rec.Code != http.StatusNotFound {
		t.Fatalf("old URL after rotate: %d", rec.Code)
	}
	newTok := (*rotated.URL)[strings.LastIndex(*rotated.URL, "/")+1:]
	eventID(t, e.send("POST", "/h/"+newTok, []byte("x")))
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE endpoint_id = $1`, ep.ID); n != 3 {
		t.Fatalf("events after rotate = %d", n)
	}

	// Delete: unknown at once, and its events are gone.
	if err := e.svc.Delete(ctx, "alice", e.project, ep.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.send("POST", "/h/"+newTok, []byte("x")); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: %d", rec.Code)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events`); n != 0 {
		t.Fatalf("events after delete = %d", n)
	}
}

func TestRejectedRequestsStoreNothing(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.endpoint(t)
	limit := config.DefaultLimits().IngestMaxBodyBytes
	if rec := e.send("POST", "/h/"+tok, make([]byte, limit+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d", rec.Code)
	}
	eventID(t, e.send("POST", "/h/"+tok, make([]byte, limit))) // exactly at the limit
	if rec := e.send("TRACE", "/h/"+tok, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("TRACE: %d", rec.Code)
	}
	if rec := e.send("POST", "/h/doesnotexist", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events`); n != 1 {
		t.Fatalf("events = %d, want only the at-limit request", n)
	}
}

func TestNotificationFollowsCommit(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.endpoint(t)
	ctx := context.Background()
	listener, err := pgx.Connect(ctx, e.DB.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close(ctx) }()
	if _, err := listener.Exec(ctx, "LISTEN events"); err != nil {
		t.Fatal(err)
	}

	id := eventID(t, e.send("POST", "/h/"+tok, []byte("x")))
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := listener.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("no notification: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event_id"] != id || payload["project_id"] != e.project.String() || len(payload) != 2 {
		t.Fatalf("payload = %s", n.Payload)
	}
	// By the time the notification arrives the event is visible.
	if c := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE id = $1`, id); c != 1 {
		t.Fatal("notified before commit")
	}
}

func TestDatabaseFailureReturns503(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.endpoint(t)
	e.Pool.Close() // simulate the database becoming unreachable
	rec := e.send("POST", "/h/"+tok, []byte("x"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"unavailable"`) || strings.Contains(string(body), "event_id") {
		t.Fatalf("body = %s", body)
	}
}

// TestAckLatencyP95 checks that a 1 KiB JSON POST is acknowledged with p95
// under 100 ms locally, over real HTTP against PostgreSQL. Timing depends on
// the machine, so it runs only with RELAYLAB_PERF=1 (task 4.3).
func TestAckLatencyP95(t *testing.T) {
	if os.Getenv("RELAYLAB_PERF") != "1" {
		t.Skip("set RELAYLAB_PERF=1 to run the ingestion latency check")
	}
	// Measure the acknowledgement path, not the rate limiter.
	e := newEnvWith(t, nil, func(o *ingestion.Options) {
		o.EndpointPerMinute, o.EndpointBurst, o.IPPerMinute, o.IPBurst = 1_000_000, 1_000_000, 1_000_000, 1_000_000
	})
	srv := httptest.NewServer(e.router)
	defer srv.Close()
	client := srv.Client()

	body := []byte(`{"data":"` + strings.Repeat("x", 1024-11) + `"}`)
	const n = 300
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			_, tok := e.endpoint(t)
			e.perfURL = srv.URL + "/h/" + tok
		}
		start := time.Now()
		resp, err := client.Post(e.perfURL, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50, p95, p99 := durations[n/2], durations[n*95/100], durations[n*99/100]
	t.Logf("1 KiB JSON POST over %d requests: p50=%v p95=%v p99=%v", n, p50, p95, p99)
	if p95 >= 100*time.Millisecond {
		t.Fatalf("p95 %v >= 100ms", p95)
	}
}

// Demo users' endpoints get the demo bucket (DEMO_INGEST_*); real users'
// endpoints keep the normal one (add-production-readiness).
func TestDemoEndpointsUseTheDemoRateLimit(t *testing.T) {
	e := newEnvWith(t, nil, func(o *ingestion.Options) { o.DemoPerMinute, o.DemoBurst = 1, 2 })
	authtest.SeedDemoUser(t, e.DB, "demo-ingest", time.Now())
	demoProject := e.Project(t, "demo-ingest", "Demo project")
	demoEp, err := e.svc.Create(context.Background(), "demo-ingest", demoProject, "Demo endpoint")
	if err != nil {
		t.Fatal(err)
	}
	demoTok := (*demoEp.URL)[strings.LastIndex(*demoEp.URL, "/")+1:]
	_, realTok := e.endpoint(t)

	codes := func(tok string, n int) []int {
		var out []int
		for range n {
			out = append(out, e.send(http.MethodPost, "/h/"+tok, []byte("x")).Code)
		}
		return out
	}
	if got := codes(demoTok, 3); got[0] != 200 || got[1] != 200 || got[2] != http.StatusTooManyRequests {
		t.Fatalf("demo endpoint: %v", got)
	}
	for i, c := range codes(realTok, 5) {
		if c != 200 {
			t.Fatalf("real endpoint request %d: %d", i, c)
		}
	}
}
