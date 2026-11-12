package events

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"relaylab/internal/realtime"
)

func waitGauge(t *testing.T, reg *prometheus.Registry, name string, want float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for gather(t, reg, name) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s = %v, want %v", name, gather(t, reg, name), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := realtime.NewMetrics(reg)
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{Metrics: m}})

	now := time.Now().UTC()
	last := a.event(t, a.project, a.ep, now.Add(-time.Minute), "POST", nil)
	for i := range 3 {
		a.event(t, a.project, a.ep, now.Add(-time.Duration(30-i)*time.Second), "POST", nil)
	}

	first := a.open(t, a.alice, a.project, nil)
	first.opened(t)
	second := a.open(t, a.alice, a.project, lastID(last.String())) // replays 3
	second.opened(t)
	for range 3 {
		second.next(t)
	}
	if got := gather(t, reg, "sse_connections"); got != 2 {
		t.Fatalf("sse_connections = %v with two streams open", got)
	}

	id := a.publish(t, a.project, a.ep, "/live")
	expectEvents(t, first, id)
	expectEvents(t, second, id)
	if got := gather(t, reg, "sse_events_sent_total"); got != 5 { // 3 replayed + 1 live on each stream
		t.Fatalf("sse_events_sent_total = %v, want 5", got)
	}
	if got := gather(t, reg, "sse_resume_backfill_total"); got != 3 {
		t.Fatalf("sse_resume_backfill_total = %v, want 3", got)
	}

	_ = first.resp.Body.Close()
	waitGauge(t, reg, "sse_connections", 1)
	_ = second.resp.Body.Close()
	waitGauge(t, reg, "sse_connections", 0)
}

func TestSlowDropMetricAndNilMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := realtime.NewMetrics(reg)
	m.SlowDrop()
	if got := gather(t, reg, "sse_slow_client_disconnects_total"); got != 1 {
		t.Fatalf("sse_slow_client_disconnects_total = %v", got)
	}
	var nilMetrics *realtime.Metrics // optional everywhere
	nilMetrics.Opened()
	nilMetrics.Sent(true)
	nilMetrics.SlowDrop()
	nilMetrics.Closed()
}

func TestStreamMetricsHaveNoLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := realtime.NewMetrics(reg)
	m.Opened()
	m.Sent(true)
	m.SlowDrop()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 4 {
		t.Fatalf("%d families, want 4", len(families))
	}
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "sse_") {
			t.Errorf("unexpected metric %s", f.GetName())
		}
		for _, s := range f.GetMetric() {
			if len(s.GetLabel()) != 0 {
				t.Errorf("%s has labels %v", f.GetName(), s.GetLabel())
			}
		}
	}
}

func gather(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		var sum float64
		for _, s := range f.GetMetric() {
			switch {
			case s.GetCounter() != nil:
				sum += s.GetCounter().GetValue()
			case s.GetGauge() != nil:
				sum += s.GetGauge().GetValue()
			}
		}
		return sum
	}
	t.Fatalf("metric %s not registered", name)
	return 0
}
