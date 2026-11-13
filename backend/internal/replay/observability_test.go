package replay

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestReplayLogsAndMetrics(t *testing.T) {
	e := newExecEnv(t)
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-Dest-Secret", "dest-header-value")
		_, _ = w.Write([]byte("response-body-text"))
	})
	reg := prometheus.NewRegistry()
	e.metrics = NewMetrics(reg)
	e.build()
	logs := &syncBuffer{}
	e.deliverer.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	id := e.queue(url+"/hook?token=query-secret", nil, []Override{{"Authorization", "Bearer override-secret"}})
	e.metrics.updateQueueDepth(context.Background(), e.Pool)
	if got := testutil.ToFloat64(e.metrics.queueDepth.WithLabelValues(JobKind)); got != 1 {
		t.Fatalf("jobs_queue_depth = %v", got)
	}
	e.runOne()
	failed := e.queue("https://unresolvable.example.test/", nil, nil)
	e.runOne()

	out := logs.String()
	for _, want := range []string{`"msg":"replay finished"`, id.String(), failed.String(), e.project.String(),
		`"destination_host":"example.com"`, `"outcome":"succeeded"`, `"outcome":"dns_failure"`, `"duration_ms"`, `"response_status":200`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
	for _, leak := range []string{"query-secret", "token=", "/hook", "override-secret", "dest-header-value", "response-body-text", "relaylab\"}"} {
		if strings.Contains(out, leak) {
			t.Errorf("log contains %q:\n%s", leak, out)
		}
	}

	if got := testutil.ToFloat64(e.metrics.replays.WithLabelValues("succeeded", "succeeded")); got != 1 {
		t.Errorf("replays_total{succeeded} = %v", got)
	}
	if got := testutil.ToFloat64(e.metrics.replays.WithLabelValues("failed", "dns_failure")); got != 1 {
		t.Errorf("replays_total{failed,dns_failure} = %v", got)
	}
	if n := testutil.CollectAndCount(e.metrics.duration); n != 1 {
		t.Errorf("replay_duration_seconds not collected (%d)", n)
	}
	families, _ := reg.Gather()
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() != "outcome" && l.GetName() != "category" && l.GetName() != "kind" {
					t.Errorf("%s has label %s", f.GetName(), l.GetName())
				}
			}
		}
	}
}
