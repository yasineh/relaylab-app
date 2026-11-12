package replay

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrUnavailable means replay cannot be used now (503 replay_unavailable).
var ErrUnavailable = errors.New("replay unavailable")

// HeartbeatWindow is how recent a worker heartbeat reporting an active
// outbound guard must be for URL replay (event-replay spec).
const HeartbeatWindow = 60 * time.Second

// Destination types.
const (
	DestinationURL  = "url"
	DestinationSink = "sink"
)

// GuardStatus reports whether some worker recently reported an active guard.
type GuardStatus interface {
	GuardActiveRecently(ctx context.Context, windowSeconds int32) (bool, error)
}

// Gate decides whether replay is available (event-replay spec, "Replay
// availability gate"). It fails closed: when the heartbeat cannot be read,
// URL replay is unavailable.
type Gate struct {
	enabled bool
	status  GuardStatus
}

// NewGate returns a gate for REPLAY_ENABLED and the heartbeat store.
func NewGate(enabled bool, status GuardStatus) *Gate { return &Gate{enabled: enabled, status: status} }

// Enabled reports REPLAY_ENABLED.
func (g *Gate) Enabled() bool { return g.enabled }

// URLAvailable reports whether URL replay is available now.
func (g *Gate) URLAvailable(ctx context.Context) (bool, error) {
	if !g.enabled {
		return false, nil
	}
	ok, err := g.status.GuardActiveRecently(ctx, int32(HeartbeatWindow/time.Second))
	if err != nil {
		return false, fmt.Errorf("read worker heartbeats: %w", err)
	}
	return ok, nil
}

// Check returns ErrUnavailable when a replay to this destination type may
// not be created. Sink replays need only REPLAY_ENABLED: they never leave
// RelayLab, so they do not depend on the outbound guard.
func (g *Gate) Check(ctx context.Context, destination string) error {
	if !g.enabled {
		return ErrUnavailable
	}
	if destination == DestinationSink {
		return nil
	}
	ok, err := g.URLAvailable(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnavailable
	}
	return nil
}
