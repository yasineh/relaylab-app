package config

import (
	"strconv"
	"strings"
)

// Replay holds the replay settings (add-event-replay, D11, D17). The API and
// the worker both load it; handlers and the worker never hard-code these
// values.
type Replay struct {
	// Enabled turns replay on. Default true in development and test, false
	// in production, where it must be set explicitly (REPLAY_ENABLED).
	Enabled bool
	// ProjectConcurrency caps replays running at once per project.
	ProjectConcurrency int
	// ProjectQueueDepth caps queued plus running replays per project
	// (429 replay_queue_full).
	ProjectQueueDepth int
	// UserRatePerMinute caps replay creations per user (429 rate_limited).
	UserRatePerMinute int
	// MaxTries caps deliveries per replay.
	MaxTries int
	// LeaseSeconds is the worker's job lease; it is extended before each try.
	LeaseSeconds int
	// MaxLeaseLosses is how many expired leases make a job dead
	// (replay failed/worker_lost).
	MaxLeaseLosses int
	// ResponseExcerptBytes caps the stored response body.
	ResponseExcerptBytes int
	// OverrideKey encrypts header override values at rest (AES-256-GCM,
	// REPLAY_OVERRIDE_ENC_KEY). Required when replay is enabled.
	OverrideKey Secret
}

type replaySpec struct {
	env      string
	def      int
	min, max int
	bind     func(*Replay) *int
}

// replaySpecs are the replay limits with their defaults and bounds.
var replaySpecs = []replaySpec{
	{"REPLAY_PROJECT_CONCURRENCY", 2, 1, 50, func(r *Replay) *int { return &r.ProjectConcurrency }},
	{"REPLAY_PROJECT_QUEUE_DEPTH", 20, 1, 10000, func(r *Replay) *int { return &r.ProjectQueueDepth }},
	{"REPLAY_USER_RATE_PER_MINUTE", 30, 1, 10000, func(r *Replay) *int { return &r.UserRatePerMinute }},
	{"REPLAY_MAX_TRIES", 3, 1, 10, func(r *Replay) *int { return &r.MaxTries }},
	{"REPLAY_LEASE_SECONDS", 60, 15, 3600, func(r *Replay) *int { return &r.LeaseSeconds }},
	{"REPLAY_MAX_LEASE_LOSSES", 3, 1, 20, func(r *Replay) *int { return &r.MaxLeaseLosses }},
	{"REPLAY_RESPONSE_EXCERPT_BYTES", 64 << 10, 1, 256 << 10, func(r *Replay) *int { return &r.ResponseExcerptBytes }},
}

// LoadReplay reads REPLAY_ENABLED, the REPLAY_* limits, and
// REPLAY_OVERRIDE_ENC_KEY.
func LoadReplay(getenv Getenv, env Env) (*Replay, error) {
	r := &Replay{Enabled: env != EnvProduction}
	switch v := strings.ToLower(strings.TrimSpace(getenv("REPLAY_ENABLED"))); v {
	case "":
	case "true", "1":
		r.Enabled = true
	case "false", "0":
		r.Enabled = false
	default:
		return nil, &FieldError{Var: "REPLAY_ENABLED", Reason: "must be true or false"}
	}
	for _, s := range replaySpecs {
		n := s.def
		if raw := strings.TrimSpace(getenv(s.env)); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < s.min || v > s.max {
				return nil, &FieldError{Var: s.env, Reason: "must be a whole number from " + strconv.Itoa(s.min) + " to " + strconv.Itoa(s.max)}
			}
			n = v
		}
		*s.bind(r) = n
	}
	if raw := strings.TrimSpace(getenv("REPLAY_OVERRIDE_ENC_KEY")); raw != "" || r.Enabled {
		key, err := decodeKey(raw)
		switch {
		case err != nil:
			return nil, &FieldError{Var: "REPLAY_OVERRIDE_ENC_KEY", Reason: err.Error()}
		case len(key) != 32:
			return nil, &FieldError{Var: "REPLAY_OVERRIDE_ENC_KEY", Reason: "must decode to exactly 32 bytes (AES-256); required when replay is enabled"}
		}
		r.OverrideKey = NewSecret(string(key))
	}
	return r, nil
}
