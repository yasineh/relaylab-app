package config

import (
	"net"
	"strings"
)

// Ingest is the API-only configuration for public webhook ingestion that is
// not a numeric limit (limits live in Limits).
type Ingest struct {
	// RedactExtraHeaders and RedactExtraParams add exact header and query
	// parameter names to the built-in redaction rules; they never remove any.
	RedactExtraHeaders []string
	RedactExtraParams  []string
	// MetricsAddr, when set, serves /metrics on a separate listener. It is
	// never served on the public port. Empty disables metrics serving.
	MetricsAddr string
}

// LoadIngest reads INGEST_REDACT_EXTRA_HEADERS, INGEST_REDACT_EXTRA_PARAMS
// (comma-separated names), and METRICS_ADDR (host:port).
func LoadIngest(getenv Getenv) (*Ingest, error) {
	in := &Ingest{
		RedactExtraHeaders: splitList(getenv("INGEST_REDACT_EXTRA_HEADERS")),
		RedactExtraParams:  splitList(getenv("INGEST_REDACT_EXTRA_PARAMS")),
		MetricsAddr:        strings.TrimSpace(getenv("METRICS_ADDR")),
	}
	for _, name := range in.RedactExtraHeaders {
		if strings.ContainsAny(name, " \t:") {
			return nil, &FieldError{Var: "INGEST_REDACT_EXTRA_HEADERS", Reason: "must be a comma-separated list of header names"}
		}
	}
	if in.MetricsAddr != "" {
		if _, _, err := net.SplitHostPort(in.MetricsAddr); err != nil {
			return nil, &FieldError{Var: "METRICS_ADDR", Reason: "must be host:port, for example 127.0.0.1:9090"}
		}
	}
	return in, nil
}
