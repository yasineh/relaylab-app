package config

import (
	"strconv"
	"strings"
	"time"
)

// Demo holds the demo-workspace settings (add-production-readiness, D16).
// Demo users are Better Auth anonymous users; the API seeds each one a
// workspace and applies these tighter limits to it.
type Demo struct {
	// Enabled is DEMO_ENABLED: the landing page offers "Try the demo". Default
	// true outside production, false in production. It must match the auth
	// service's setting, which owns the anonymous sign-in endpoint.
	Enabled bool
	// TTL is DEMO_TTL: a demo workspace and its user expire this long after
	// creation (default 24h). Expired demo sessions are refused at once; the
	// auth service deletes expired demo users and, by cascade, their data.
	TTL time.Duration
	// Limits for demo users (the central limits module, D17).
	MaxProjects     int
	MaxEndpoints    int
	MaxEvents       int // per demo project, enforced by the retention purge
	MaxStreams      int
	IngestPerMinute int // per demo endpoint
	IngestBurst     int
}

type demoSpec struct {
	env      string
	def      int
	min, max int
	bind     func(*Demo) *int
}

var demoSpecs = []demoSpec{
	{"DEMO_MAX_PROJECTS", 1, 1, 10, func(d *Demo) *int { return &d.MaxProjects }},
	{"DEMO_MAX_ENDPOINTS", 2, 2, 10, func(d *Demo) *int { return &d.MaxEndpoints }},
	{"DEMO_MAX_EVENTS", 200, 10, 10000, func(d *Demo) *int { return &d.MaxEvents }},
	{"DEMO_MAX_STREAMS", 2, 1, 10, func(d *Demo) *int { return &d.MaxStreams }},
	{"DEMO_INGEST_RATE_PER_MINUTE", 10, 1, 600, func(d *Demo) *int { return &d.IngestPerMinute }},
	{"DEMO_INGEST_BURST", 5, 1, 100, func(d *Demo) *int { return &d.IngestBurst }},
}

// LoadDemo reads DEMO_ENABLED, DEMO_TTL, and the DEMO_* limits.
func LoadDemo(getenv Getenv, env Env) (*Demo, error) {
	d := &Demo{TTL: 24 * time.Hour}
	var err error
	if d.Enabled, err = parseBool(getenv("DEMO_ENABLED"), env != EnvProduction); err != nil {
		return nil, &FieldError{Var: "DEMO_ENABLED", Reason: err.Error()}
	}
	if raw := strings.TrimSpace(getenv("DEMO_TTL")); raw != "" {
		v, err := time.ParseDuration(raw)
		if err != nil || v < time.Minute || v > 7*24*time.Hour || v%time.Minute != 0 {
			return nil, &FieldError{Var: "DEMO_TTL", Reason: "must be a duration in whole minutes from 1m to 168h, such as 24h"}
		}
		d.TTL = v
	}
	for _, s := range demoSpecs {
		n := s.def
		if raw := strings.TrimSpace(getenv(s.env)); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < s.min || v > s.max {
				return nil, &FieldError{Var: s.env, Reason: "must be a whole number from " + strconv.Itoa(s.min) + " to " + strconv.Itoa(s.max)}
			}
			n = v
		}
		*s.bind(d) = n
	}
	return d, nil
}
