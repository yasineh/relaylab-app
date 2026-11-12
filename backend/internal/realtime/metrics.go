package realtime

import "github.com/prometheus/client_golang/prometheus"

// Metrics are the live stream collectors. None has labels: no user, project,
// or endpoint ids, so cardinality is fixed. A nil *Metrics records nothing.
type Metrics struct {
	connections prometheus.Gauge
	sent        prometheus.Counter
	backfilled  prometheus.Counter
	slowDrops   prometheus.Counter
}

// NewMetrics registers the stream collectors on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		connections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sse_connections",
			Help: "Open live event streams on this instance.",
		}),
		sent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sse_events_sent_total",
			Help: "event.created messages sent on live streams, live and replayed.",
		}),
		backfilled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sse_resume_backfill_total",
			Help: "Events replayed to reconnecting streams from Last-Event-ID.",
		}),
		slowDrops: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sse_slow_client_disconnects_total",
			Help: "Streams dropped because the client fell too far behind.",
		}),
	}
	reg.MustRegister(m.connections, m.sent, m.backfilled, m.slowDrops)
	return m
}

// Opened records a stream starting; call Closed when it ends.
func (m *Metrics) Opened() {
	if m != nil {
		m.connections.Inc()
	}
}

// Closed records a stream ending.
func (m *Metrics) Closed() {
	if m != nil {
		m.connections.Dec()
	}
}

// Sent records one event.created message; backfill marks a replayed one.
func (m *Metrics) Sent(backfill bool) {
	if m == nil {
		return
	}
	m.sent.Inc()
	if backfill {
		m.backfilled.Inc()
	}
}

// SlowDrop records a subscriber dropped for a full buffer. Pass it to the
// broker as Options.OnSlowDrop.
func (m *Metrics) SlowDrop() {
	if m != nil {
		m.slowDrops.Inc()
	}
}
