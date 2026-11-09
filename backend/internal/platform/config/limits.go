package config

import (
	"errors"
	"strconv"
	"strings"
)

// Limits holds every product limit (D17). Backend code must read limits only
// from this struct; a test fails if a known limit value is hard-coded elsewhere.
// Later changes add their keys to limitSpecs with a documented default.
type Limits struct {
	// HTTPMaxHeaderBytes caps the total size of request headers (431 when exceeded).
	HTTPMaxHeaderBytes int
	// APIMaxBodyBytes caps dashboard JSON request bodies (413 when exceeded).
	// It also caps bodies proxied to the auth service under /api/auth.
	APIMaxBodyBytes int
	// AuthProxyTimeoutSeconds bounds a proxied /api/auth request end to end (502/504 when exceeded).
	AuthProxyTimeoutSeconds int
	// ProjectsPerUser caps how many projects one user may own (409 project_limit_reached).
	ProjectsPerUser int
	// EndpointsPerProject caps endpoints per project (409 endpoint_limit_reached).
	EndpointsPerProject int
	// IngestMaxBodyBytes caps a received webhook body (413 payload_too_large).
	// Requests at exactly the limit are accepted. Never above 1 MiB.
	IngestMaxBodyBytes int
	// IngestMaxHeaderFields caps the number of header fields on a received
	// webhook (431). Header bytes are capped by HTTPMaxHeaderBytes.
	IngestMaxHeaderFields int
	// IngestBodyTimeoutSeconds bounds how long a webhook body may take to arrive (408).
	IngestBodyTimeoutSeconds int
	// IngestRatePerEndpointPerMinute and IngestRateEndpointBurst set the
	// per-endpoint token bucket (429 rate_limited). Limits apply per instance.
	IngestRatePerEndpointPerMinute int
	IngestRateEndpointBurst        int
	// IngestRatePerIPPerMinute and IngestRateIPBurst set the per-client-IP bucket,
	// applied before the endpoint lookup so unknown-token floods are bounded too.
	IngestRatePerIPPerMinute int
	IngestRateIPBurst        int
}

type limitSpec struct {
	env  string
	def  int
	max  int // 0 means no upper bound
	doc  string
	bind func(*Limits) *int
}

// limitSpecs is the single registry of limits, their env overrides, and defaults.
var limitSpecs = []limitSpec{
	{
		env:  "LIMIT_HTTP_MAX_HEADER_BYTES",
		def:  32 << 10,
		doc:  "Maximum total request header size in bytes",
		bind: func(l *Limits) *int { return &l.HTTPMaxHeaderBytes },
	},
	{
		env:  "LIMIT_API_MAX_BODY_BYTES",
		def:  64 << 10,
		doc:  "Maximum dashboard API JSON body size in bytes",
		bind: func(l *Limits) *int { return &l.APIMaxBodyBytes },
	},
	{
		env:  "LIMIT_AUTH_PROXY_TIMEOUT_SECONDS",
		def:  10,
		doc:  "Maximum time for a request proxied to the auth service, in seconds",
		bind: func(l *Limits) *int { return &l.AuthProxyTimeoutSeconds },
	},
	{
		env:  "LIMIT_PROJECTS_PER_USER",
		def:  20,
		doc:  "Maximum number of projects one user may own",
		bind: func(l *Limits) *int { return &l.ProjectsPerUser },
	},
	{
		env:  "LIMIT_ENDPOINTS_PER_PROJECT",
		def:  25,
		doc:  "Maximum number of webhook endpoints in one project",
		bind: func(l *Limits) *int { return &l.EndpointsPerProject },
	},
	{
		env:  "LIMIT_INGEST_MAX_BODY_BYTES",
		def:  256 << 10,
		max:  1 << 20,
		doc:  "Maximum received webhook body size in bytes (at most 1048576)",
		bind: func(l *Limits) *int { return &l.IngestMaxBodyBytes },
	},
	{
		env:  "LIMIT_INGEST_MAX_HEADER_FIELDS",
		def:  100,
		doc:  "Maximum number of header fields on a received webhook",
		bind: func(l *Limits) *int { return &l.IngestMaxHeaderFields },
	},
	{
		env:  "LIMIT_INGEST_BODY_TIMEOUT_SECONDS",
		def:  10,
		doc:  "Maximum time for a received webhook body to arrive, in seconds",
		bind: func(l *Limits) *int { return &l.IngestBodyTimeoutSeconds },
	},
	{
		env:  "LIMIT_INGEST_RATE_PER_ENDPOINT_PER_MINUTE",
		def:  60,
		doc:  "Sustained accepted requests per endpoint per minute, per instance",
		bind: func(l *Limits) *int { return &l.IngestRatePerEndpointPerMinute },
	},
	{
		env:  "LIMIT_INGEST_RATE_ENDPOINT_BURST",
		def:  30,
		doc:  "Burst of requests one endpoint may receive at once, per instance",
		bind: func(l *Limits) *int { return &l.IngestRateEndpointBurst },
	},
	{
		env:  "LIMIT_INGEST_RATE_PER_IP_PER_MINUTE",
		def:  120,
		doc:  "Sustained ingestion requests per client IP per minute, per instance",
		bind: func(l *Limits) *int { return &l.IngestRatePerIPPerMinute },
	},
	{
		env:  "LIMIT_INGEST_RATE_IP_BURST",
		def:  60,
		doc:  "Burst of ingestion requests one client IP may send at once, per instance",
		bind: func(l *Limits) *int { return &l.IngestRateIPBurst },
	},
}

// DefaultLimits returns the documented defaults.
func DefaultLimits() Limits {
	var l Limits
	for _, s := range limitSpecs {
		*s.bind(&l) = s.def
	}
	return l
}

// LoadLimits applies LIMIT_* overrides on top of the defaults. Each override
// must be a positive integer; errors name the variable, never the value.
func LoadLimits(getenv Getenv) (Limits, error) {
	l := DefaultLimits()
	var errs []error
	for _, s := range limitSpecs {
		raw := strings.TrimSpace(getenv(s.env))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			errs = append(errs, &FieldError{Var: s.env, Reason: "must be a positive integer"})
			continue
		}
		if s.max > 0 && n > s.max {
			errs = append(errs, &FieldError{Var: s.env, Reason: "must be at most " + strconv.Itoa(s.max)})
			continue
		}
		*s.bind(&l) = n
	}
	if len(errs) > 0 {
		return Limits{}, errors.Join(errs...)
	}
	return l, nil
}

// LimitDoc describes one limit for documentation output.
type LimitDoc struct {
	Env     string
	Default int
	Doc     string
}

// LimitDocs lists all limits with defaults, for docs generation and tests.
func LimitDocs() []LimitDoc {
	out := make([]LimitDoc, 0, len(limitSpecs))
	for _, s := range limitSpecs {
		out = append(out, LimitDoc{Env: s.env, Default: s.def, Doc: s.doc})
	}
	return out
}
