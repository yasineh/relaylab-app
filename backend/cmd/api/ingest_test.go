package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"relaylab/internal/endpoints"
	"relaylab/internal/endpoints/token"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/redact"
	"relaylab/internal/testutil/fixture"
)

// TestIngestionIgnoresDashboardSession sends a valid dashboard session cookie
// to an ingestion URL through the production router: ingestion must not look
// the session up, must grant nothing, and must store the cookie redacted.
func TestIngestionIgnoresDashboardSession(t *testing.T) {
	fx := fixture.New(t, "alice")
	keys, err := token.NewKeys(bytes.Repeat([]byte{0xA1}, 32), bytes.Repeat([]byte{0xB2}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	limits := config.DefaultLimits()
	ep, err := endpoints.NewService(fx.Pool, keys, appOrigin, limits.EndpointsPerProject).
		Create(context.Background(), "alice", fx.Project(t, "alice", "p"), "e")
	if err != nil {
		t.Fatal(err)
	}
	h := testRouterWith(t, routerOpts{ingestion: ingestion.NewHandler(ingestion.Options{
		Store: ingestion.NewPGStore(fx.Pool), Hasher: keys, Redactor: redact.New(nil, nil),
		Metrics: ingestion.NewMetrics(prometheus.NewRegistry()), MaxBodyBytes: limits.IngestMaxBodyBytes,
		MaxHeaderFields: limits.IngestMaxHeaderFields, BodyTimeout: 10 * time.Second,
		EndpointPerMinute: 60, EndpointBurst: 30, IPPerMinute: 120, IPBurst: 60,
	})})

	before := sessionLookups.Load()
	req := httptest.NewRequest(http.MethodPost, strings.TrimPrefix(*ep.URL, appOrigin), strings.NewReader(`{"a":1}`))
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(validCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if n := sessionLookups.Load() - before; n != 0 {
		t.Fatalf("ingestion looked up the session %d times", n)
	}
	for _, name := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"} {
		if v := rec.Header().Get(name); v != "" {
			t.Errorf("ingestion response sets %s: %q", name, v)
		}
	}
	var headers string
	if err := fx.Admin.QueryRow(context.Background(), `SELECT headers::text FROM app.webhook_events`).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(headers, `["Cookie", "[REDACTED]"]`) || strings.Contains(headers, testToken) {
		t.Fatalf("stored headers = %s", headers)
	}

	// The same request grants nothing on the dashboard API without going through it.
	req = httptest.NewRequest(http.MethodGet, strings.TrimPrefix(*ep.URL, appOrigin)+"/../../api/v1/me", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "u1@example.test") {
		t.Fatal("ingestion path exposed dashboard data")
	}
}

// TestMetricsAreNotOnThePublicPort checks /metrics is not served by the main router.
func TestMetricsAreNotOnThePublicPort(t *testing.T) {
	h := testRouter(t, fakeSPA) // even with the web app mounted, which answers other unknown paths
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "not_found" {
		t.Fatalf("/metrics on the public port: %d %s", rec.Code, rec.Body)
	}
}

// TestMetricsMuxServesIngestionCounters checks the private listener's handler.
func TestMetricsMuxServesIngestionCounters(t *testing.T) {
	reg := prometheus.NewRegistry()
	ingestion.NewMetrics(reg)
	rec := httptest.NewRecorder()
	metricsMux(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `ingest_requests_total{outcome="accepted"} 0`) {
		t.Fatalf("status %d body %.300s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	metricsMux(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/h/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("metrics listener serves other paths: %d", rec.Code)
	}
}

func TestSeparateIngestHost(t *testing.T) {
	if got := separateIngestHost("relaylab.test", "RelayLab.test"); got != "" {
		t.Fatalf("same host: %q", got)
	}
	if got := separateIngestHost("in.relaylab.test", "relaylab.test"); got != "in.relaylab.test" {
		t.Fatalf("separate host: %q", got)
	}
}
