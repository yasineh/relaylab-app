package ingestion

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Outcomes label every ingestion request. They are the only metric label, so
// cardinality stays fixed (no endpoint ids or paths).
const (
	outcomeAccepted        = "accepted"
	outcomeRejectedSize    = "rejected_size"    // 413, 414, 431
	outcomeRejectedRate    = "rejected_rate"    // 429
	outcomeRejectedMethod  = "rejected_method"  // 405
	outcomeRejectedTimeout = "rejected_timeout" // 408
	outcomeRejectedInvalid = "rejected_invalid" // 400: the body could not be read
	outcomeNotFound        = "not_found"        // 404
	outcomeDisabled        = "disabled"         // 410
	outcomeError           = "error"            // 503: lookup or persistence failed
)

var allOutcomes = []string{
	outcomeAccepted, outcomeRejectedSize, outcomeRejectedRate, outcomeRejectedMethod,
	outcomeRejectedTimeout, outcomeRejectedInvalid, outcomeNotFound, outcomeDisabled, outcomeError,
}

// Metrics are the ingestion Prometheus collectors.
type Metrics struct {
	requests *prometheus.CounterVec
	duration prometheus.Histogram
	bodySize prometheus.Histogram
}

// NewMetrics registers the ingestion collectors on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_requests_total",
			Help: "Webhook ingestion requests by outcome.",
		}, []string{"outcome"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ingest_duration_seconds",
			Help:    "Time from receiving a webhook request to responding.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		bodySize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ingest_body_bytes",
			Help:    "Body size of accepted webhook requests.",
			Buckets: prometheus.ExponentialBuckets(64, 4, 8), // 64 B .. 1 MiB
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.bodySize)
	// Pre-create every outcome so dashboards see zeros instead of gaps.
	for _, o := range allOutcomes {
		m.requests.WithLabelValues(o)
	}
	return m
}
