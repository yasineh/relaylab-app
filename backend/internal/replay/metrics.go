package replay

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"relaylab/internal/platform/db/store"
)

// Metrics are the replay and job queue collectors. Labels are bounded sets
// (outcome, category, kind): never project, endpoint, or replay ids. A nil
// *Metrics records nothing.
type Metrics struct {
	replays    *prometheus.CounterVec
	duration   prometheus.Histogram
	queueDepth *prometheus.GaugeVec
	dead       prometheus.Counter
}

// NewMetrics registers the replay collectors on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		replays: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "replays_total",
			Help: "Finished replays by outcome (succeeded or failed) and category.",
		}, []string{"outcome", "category"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "replay_duration_seconds",
			Help:    "Time to deliver a replay, all tries included.",
			Buckets: prometheus.ExponentialBuckets(0.01, 3, 9), // 10 ms .. ~66 s
		}),
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "jobs_queue_depth",
			Help: "Queued background jobs by kind.",
		}, []string{"kind"}),
		dead: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_dead_total",
			Help: "Jobs that lost their lease too many times and were abandoned.",
		}),
	}
	reg.MustRegister(m.replays, m.duration, m.queueDepth, m.dead)
	m.queueDepth.WithLabelValues(JobKind)
	return m
}

func (m *Metrics) replayDone(state, category string, d time.Duration) {
	if m == nil {
		return
	}
	m.replays.WithLabelValues(state, category).Inc()
	if d > 0 {
		m.duration.Observe(d.Seconds())
	}
}

func (m *Metrics) jobDead() {
	if m != nil {
		m.dead.Inc()
	}
}

func (m *Metrics) updateQueueDepth(ctx context.Context, pool *pgxpool.Pool) {
	if m == nil {
		return
	}
	if n, err := store.New(pool).CountQueuedJobs(ctx, JobKind); err == nil {
		m.queueDepth.WithLabelValues(JobKind).Set(float64(n))
	}
}
