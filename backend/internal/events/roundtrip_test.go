package events

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"

	"relaylab/internal/endpoints"
	"relaylab/internal/endpoints/token"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/redact"
	"relaylab/internal/testutil/fixture"
)

// TestCredentialsNeverReachTheAPI sends a real webhook with credentials in
// headers, cookies, and the query through ingestion, then reads it back
// through every event route. No response may contain an original value.
func TestCredentialsNeverReachTheAPI(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	keys, err := token.NewKeys(bytes.Repeat([]byte{0xA1}, 32), bytes.Repeat([]byte{0xB2}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	limits := config.DefaultLimits()
	ep, err := endpoints.NewService(a.Pool, keys, fixture.Origin, limits.EndpointsPerProject).Create(ctx, "alice", a.project, "Live")
	if err != nil {
		t.Fatal(err)
	}
	red := redact.New(nil, nil)
	ingest := chi.NewRouter()
	ingest.Use(httpx.RequestIDMiddleware(slog.New(slog.DiscardHandler))) // as in production: every event has a correlation id
	ingestion.NewHandler(ingestion.Options{
		Store: ingestion.NewPGStore(a.Pool), Hasher: keys, Redactor: red,
		Metrics: ingestion.NewMetrics(prometheus.NewRegistry()), MaxBodyBytes: limits.IngestMaxBodyBytes,
		MaxHeaderFields: limits.IngestMaxHeaderFields, BodyTimeout: 10 * time.Second,
		EndpointPerMinute: 60, EndpointBurst: 30, IPPerMinute: 120, IPBurst: 60,
	}).Routes(ingest)

	secrets := []string{"Bearer abc123-secret", "sess-cookie-value-9f", "qs-token-77e1", "sig-hmac-42ab"}
	path := strings.TrimPrefix(*ep.URL, fixture.Origin) + "/orders?token=qs-token-77e1&page=2"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"order":"A-1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer abc123-secret")
	req.Header.Set("Cookie", "session=sess-cookie-value-9f")
	req.Header.Set("X-Signature", "sig-hmac-42ab")
	rec := httptest.NewRecorder()
	ingest.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", rec.Code, rec.Body)
	}
	var ack struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil || ack.EventID == "" {
		t.Fatalf("ack %s: %v", rec.Body, err)
	}

	responses := map[string]string{}
	for _, p := range []string{
		a.base(a.project),
		a.base(a.project) + "/" + ack.EventID,
		a.base(a.project) + "/" + ack.EventID + "?reveal=headers",
		a.base(a.project) + "/" + ack.EventID + "/body",
	} {
		r := a.alice.Do(http.MethodGet, p, "")
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", p, r.Code, r.Body)
		}
		responses[p] = r.Body.String()
	}
	for p, body := range responses {
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s contains %q", p, s)
			}
		}
	}

	d := decodeJSON[detailJSON](t, []byte(responses[a.base(a.project)+"/"+ack.EventID]))
	flagged := map[string]bool{}
	for _, h := range d.Headers {
		if h.Redacted {
			flagged[h.Name] = h.Value == "[REDACTED]"
		}
	}
	for _, name := range []string{"Authorization", "Cookie", "X-Signature"} {
		if !flagged[name] {
			t.Errorf("%s not shown as removed: %+v", name, d.Headers)
		}
	}
	if d.RedactedCount != 4 || d.Path != "/orders" || *d.Body.Text != `{"order":"A-1"}` {
		t.Fatalf("detail: count %d path %q body %v", d.RedactedCount, d.Path, d.Body.Text)
	}
	var tokenFlagged bool
	for _, q := range d.Query {
		if q.Name == "token" {
			tokenFlagged = q.Redacted && q.Value == "[REDACTED]"
		}
	}
	if !tokenFlagged || strings.Contains(d.RawQuery, "qs-token") {
		t.Fatalf("query: %+v raw %q", d.Query, d.RawQuery)
	}
}
