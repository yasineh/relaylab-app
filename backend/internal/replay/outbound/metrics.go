package outbound

import "github.com/prometheus/client_golang/prometheus"

// allCategories lists every outcome, so the counter starts at zero for each.
var allCategories = []Category{
	Succeeded, InvalidURL, BlockedScheme, BlockedPort, BlockedHost, BlockedAddress, DNSFailure,
	ConnectFailure, TLSFailure, Timeout, ResponseTooLarge, CapacityExceeded, InvalidRequest, GuardInactive,
}

// Metrics counts guarded requests by outcome. The category is the only label:
// no host, project, or URL, so cardinality is fixed. A nil *Metrics records
// nothing.
type Metrics struct {
	requests *prometheus.CounterVec
}

// NewMetrics registers outbound_requests_total on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{requests: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "outbound_requests_total",
		Help: "User-directed outbound requests (replay) by outcome category.",
	}, []string{"category"})}
	reg.MustRegister(m.requests)
	for _, c := range allCategories {
		m.requests.WithLabelValues(string(c))
	}
	return m
}

func (m *Metrics) observe(c Category) {
	if m != nil {
		m.requests.WithLabelValues(string(c)).Inc()
	}
}
