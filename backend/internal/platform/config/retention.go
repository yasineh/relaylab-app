package config

import (
	"strconv"
	"strings"
	"time"
)

// Retention holds the data retention settings (add-event-retention, D10,
// D17). The worker's purge loop reads them, and the API exposes EventTTL and
// MaxEventsPerProject on GET /api/v1/config.
type Retention struct {
	// EventTTL is how long webhook events (and their replays) are kept.
	// Default 7 days. 0 disables time-based purge, which is allowed only
	// outside production. A demo deployment sets 24h.
	EventTTL time.Duration
	// MaxEventsPerProject caps stored events per project; the oldest beyond
	// the cap are deleted on each purge run.
	MaxEventsPerProject int
	// AuditTTL is how long audit log entries are kept.
	AuditTTL time.Duration
	// JobTTL is how long finished (done or dead) background jobs are kept.
	JobTTL time.Duration
	// Interval is the time between purge runs.
	Interval time.Duration
}

type durationSpec struct {
	env      string
	def      time.Duration
	min, max time.Duration
	bind     func(*Retention) *time.Duration
}

var retentionDurations = []durationSpec{
	{"RETENTION_EVENT_TTL", 7 * 24 * time.Hour, time.Hour, 365 * 24 * time.Hour, func(r *Retention) *time.Duration { return &r.EventTTL }},
	{"RETENTION_AUDIT_TTL", 90 * 24 * time.Hour, 24 * time.Hour, 3 * 365 * 24 * time.Hour, func(r *Retention) *time.Duration { return &r.AuditTTL }},
	{"RETENTION_JOB_TTL", 7 * 24 * time.Hour, time.Hour, 365 * 24 * time.Hour, func(r *Retention) *time.Duration { return &r.JobTTL }},
	{"RETENTION_INTERVAL", 10 * time.Minute, time.Minute, 24 * time.Hour, func(r *Retention) *time.Duration { return &r.Interval }},
}

const (
	retentionMaxEventsEnv     = "RETENTION_MAX_EVENTS_PER_PROJECT"
	retentionMaxEventsDefault = 10000
	retentionMaxEventsMax     = 1000000
)

// LoadRetention reads the RETENTION_* settings. Durations use Go syntax
// ("24h", "168h", "10m"). RETENTION_EVENT_TTL=0 turns off time-based event
// purge for local debugging and is rejected in production.
func LoadRetention(getenv Getenv, env Env) (*Retention, error) {
	r := &Retention{MaxEventsPerProject: retentionMaxEventsDefault}
	for _, s := range retentionDurations {
		d := s.def
		if raw := strings.TrimSpace(getenv(s.env)); raw != "" {
			v, err := time.ParseDuration(raw)
			switch {
			case err == nil && v == 0 && s.env == "RETENTION_EVENT_TTL" && env != EnvProduction:
			case err != nil || v < s.min || v > s.max:
				reason := "must be a duration from " + s.min.String() + " to " + s.max.String()
				if s.env == "RETENTION_EVENT_TTL" {
					reason += " (0 disables purge outside production only)"
				}
				return nil, &FieldError{Var: s.env, Reason: reason}
			}
			d = v
		}
		*s.bind(r) = d
	}
	if raw := strings.TrimSpace(getenv(retentionMaxEventsEnv)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > retentionMaxEventsMax {
			return nil, &FieldError{Var: retentionMaxEventsEnv, Reason: "must be a whole number from 1 to " + strconv.Itoa(retentionMaxEventsMax)}
		}
		r.MaxEventsPerProject = n
	}
	return r, nil
}
