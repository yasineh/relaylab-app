package replay

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/platform/db/store"
)

// heartbeatInterval is how often a worker reports its guard status. The API
// accepts heartbeats up to HeartbeatWindow old.
const heartbeatInterval = 15 * time.Second

// GuardState reports the outbound guard's self-check (outbound.Client).
type GuardState interface{ Active() bool }

// Heartbeat writes this worker's outbound guard status to
// app.worker_heartbeats, which the API's availability gate reads.
type Heartbeat struct {
	pool     *pgxpool.Pool
	workerID string
	version  string
	guard    GuardState
	logger   *slog.Logger
}

// NewHeartbeat returns a heartbeat writer for one worker.
func NewHeartbeat(pool *pgxpool.Pool, workerID, version string, guard GuardState, logger *slog.Logger) *Heartbeat {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if len(version) > 64 {
		version = version[:64]
	}
	return &Heartbeat{pool: pool, workerID: workerID, version: version, guard: guard, logger: logger}
}

// Beat records the current status once and removes rows of long-gone workers.
func (h *Heartbeat) Beat(ctx context.Context) error {
	status := "inactive"
	if h.guard.Active() {
		status = "active"
	}
	q := store.New(h.pool)
	if err := q.UpsertWorkerHeartbeat(ctx, store.UpsertWorkerHeartbeatParams{WorkerID: h.workerID, OutboundGuard: status, Version: h.version}); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	return q.DeleteStaleWorkerHeartbeats(ctx)
}

// Run beats immediately and then every 15 seconds until ctx is done.
func (h *Heartbeat) Run(ctx context.Context) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		if err := h.Beat(ctx); err != nil && ctx.Err() == nil {
			h.logger.Error("worker heartbeat failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
