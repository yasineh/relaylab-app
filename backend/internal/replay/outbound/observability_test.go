package outbound

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestOutcomeCounterAndLogFields(t *testing.T) {
	e := newEnv(t)
	reg := prometheus.NewRegistry()
	e.opts.Metrics = NewMetrics(reg)
	var logs bytes.Buffer
	e.opts.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	e.build()

	e.do(http.MethodPost, "https://example.com:"+port+"/hook", nil, nil)
	e.do(http.MethodGet, "https://169.254.169.254/", nil, nil)
	e.do(http.MethodGet, "https://metadata.google.internal/", nil, nil)
	e.do(http.MethodGet, "https://169.254.169.254/again", nil, nil)

	for c, want := range map[Category]float64{Succeeded: 1, BlockedAddress: 2, BlockedHost: 1, Timeout: 0} {
		if got := testutil.ToFloat64(e.opts.Metrics.requests.WithLabelValues(string(c))); got != want {
			t.Errorf("outbound_requests_total{category=%q} = %v, want %v", c, got, want)
		}
	}
	families, _ := reg.Gather()
	for _, f := range families {
		for _, m := range f.GetMetric() {
			if len(m.GetLabel()) != 1 || m.GetLabel()[0].GetName() != "category" {
				t.Fatalf("%s has labels %v; only category is allowed", f.GetName(), m.GetLabel())
			}
		}
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	var entries []map[string]any
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %s", l)
		}
		if m["msg"] == "outbound request" {
			entries = append(entries, m)
		}
	}
	if len(entries) != 4 {
		t.Fatalf("%d log entries: %s", len(entries), logs.String())
	}
	ok := entries[0]
	if ok["category"] != "succeeded" || ok["host"] != "example.com" || ok["method"] != "POST" ||
		ok["status"] != float64(202) || ok["truncated"] != false || ok["duration_ms"] == nil {
		t.Errorf("success entry %v", ok)
	}
	if b := entries[2]; b["category"] != "blocked_host" || b["host"] != "metadata.google.internal" {
		t.Errorf("blocked entry %v", b)
	}
}
