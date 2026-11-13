package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
)

// Queue timing (design: Queue mechanics).
const (
	pollInterval  = time.Second      // claim attempt when idle, besides LISTEN wake-ups
	reapInterval  = 15 * time.Second // expired-lease sweep
	claimBatch    = 16               // candidates locked per claim transaction
	notifyChannel = "replay_jobs"
)

// Job is a claimed job. The holder must call Extend before each piece of
// work that could outlast the lease.
type Job struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	ReplayID  uuid.UUID
	Attempts  int
	owner     string
	runner    *Runner
}

// ErrLeaseLost means another worker may now own the job: stop and write
// nothing.
var ErrLeaseLost = errors.New("job lease lost")

// Extend renews the lease. ErrLeaseLost means the job was reaped or deleted.
func (j Job) Extend(ctx context.Context) error {
	n, err := store.New(j.runner.pool).ExtendJobLease(ctx, store.ExtendJobLeaseParams{
		ID: pgUUID(j.ID), LeaseOwner: pgText(j.owner), LeaseSeconds: int32(j.runner.limits.LeaseSeconds), //nolint:gosec // bounded by config
	})
	if err != nil {
		return fmt.Errorf("extend lease: %w", err)
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Executor runs one claimed job. It finishes the job itself (see
// Runner.Finish); returning means the slot is free.
type Executor interface {
	Execute(ctx context.Context, job Job)
}

// Runner claims replay jobs and runs them with bounded concurrency.
type Runner struct {
	pool     *pgxpool.Pool
	listenDB *pgxpool.Pool // direct connection for LISTEN
	id       string
	limits   *config.Replay
	slots    int
	exec     Executor
	logger   *slog.Logger
	metrics  *Metrics
	wake     chan struct{}
	poll     time.Duration
	reapTick time.Duration
}

// RunnerOptions configure a Runner.
type RunnerOptions struct {
	Pool *pgxpool.Pool
	// ListenPool is a direct (unpooled) connection pool for LISTEN; nil
	// means Pool, which is right unless Pool goes through a transaction pooler.
	ListenPool *pgxpool.Pool
	WorkerID   string
	Limits     *config.Replay
	Slots      int // concurrent jobs in this process (outbound concurrency)
	Executor   Executor
	Logger     *slog.Logger
	Metrics    *Metrics

	poll, reap time.Duration // tests
}

// NewRunner returns a runner; call Run to start it.
func NewRunner(o RunnerOptions) *Runner {
	r := &Runner{
		pool: o.Pool, listenDB: o.ListenPool, id: o.WorkerID, limits: o.Limits, slots: max(o.Slots, 1), exec: o.Executor,
		logger: o.Logger, metrics: o.Metrics, wake: make(chan struct{}, 1), poll: o.poll, reapTick: o.reap,
	}
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}
	if r.listenDB == nil {
		r.listenDB = r.pool
	}
	if r.poll <= 0 {
		r.poll = pollInterval
	}
	if r.reapTick <= 0 {
		r.reapTick = reapInterval
	}
	return r
}

// Claim takes the oldest runnable job whose project has fewer than
// REPLAY_PROJECT_CONCURRENCY running jobs. Rows locked by other workers are
// skipped, and a per-project advisory lock makes the running count exact.
func (r *Runner) Claim(ctx context.Context) (Job, bool, error) {
	var job Job
	found := false
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		cands, err := q.LockQueuedJobs(ctx, store.LockQueuedJobsParams{Kind: JobKind, RowLimit: claimBatch})
		if err != nil {
			return fmt.Errorf("lock queued jobs: %w", err)
		}
		busy := map[uuid.UUID]bool{}
		for _, c := range cands {
			project := uuid.UUID(c.ProjectID.Bytes)
			if busy[project] {
				continue
			}
			if c.ProjectID.Valid {
				if err := q.LockProjectJobs(ctx, c.ProjectID); err != nil {
					return fmt.Errorf("lock project: %w", err)
				}
				running, err := q.CountRunningJobs(ctx, c.ProjectID)
				if err != nil {
					return fmt.Errorf("count running: %w", err)
				}
				if int(running) >= r.limits.ProjectConcurrency {
					busy[project] = true
					continue
				}
			}
			row, err := q.MarkJobRunning(ctx, store.MarkJobRunningParams{
				ID: c.ID, LeaseOwner: pgText(r.id), LeaseSeconds: int32(r.limits.LeaseSeconds), //nolint:gosec // bounded by config
			})
			if err != nil {
				return fmt.Errorf("mark running: %w", err)
			}
			var payload struct {
				ReplayID uuid.UUID `json:"replay_id"`
			}
			_ = json.Unmarshal(row.Payload, &payload)
			job = Job{
				ID: row.ID.Bytes, ProjectID: row.ProjectID.Bytes, ReplayID: payload.ReplayID,
				Attempts: int(row.Attempts), owner: r.id, runner: r,
			}
			found = true
			return nil
		}
		return nil
	})
	return job, found, err
}

// Finish marks a job done (or dead) if this worker still holds its lease.
func (r *Runner) Finish(ctx context.Context, q *store.Queries, job Job, state, lastError string) (bool, error) {
	n, err := q.FinishJob(ctx, store.FinishJobParams{
		ID: pgUUID(job.ID), LeaseOwner: pgText(job.owner), State: state, LastError: optText(lastError),
	})
	if err != nil {
		return false, fmt.Errorf("finish job: %w", err)
	}
	return n > 0, nil
}

// Reap returns expired leases to the queue. Jobs that lost their lease
// MaxLeaseLosses times become dead and their replay ends failed/worker_lost.
func (r *Runner) Reap(ctx context.Context) error {
	q := store.New(r.pool)
	rows, err := q.ReapExpiredJobs(ctx)
	if err != nil {
		return fmt.Errorf("reap jobs: %w", err)
	}
	for _, j := range rows {
		if j.State != "dead" || j.Kind != JobKind {
			r.logger.Info("job lease expired; requeued", slog.String("job_id", uuid.UUID(j.ID.Bytes).String()))
			continue
		}
		r.metrics.jobDead()
		var payload struct {
			ReplayID uuid.UUID `json:"replay_id"`
		}
		_ = json.Unmarshal(j.Payload, &payload)
		if _, err := q.FailReplayWorkerLost(ctx, store.FailReplayWorkerLostParams{
			ID: pgUUID(payload.ReplayID), FailureMessage: pgText(workerLostMessage),
		}); err != nil {
			return fmt.Errorf("finalize worker_lost: %w", err)
		}
		r.metrics.replayDone("failed", CategoryWorkerLost, 0)
		r.logger.Warn("job dead after repeated lease losses",
			slog.String("job_id", uuid.UUID(j.ID.Bytes).String()), slog.String("replay_id", payload.ReplayID.String()))
	}
	return nil
}

// Wake makes the runner try to claim now (called on NOTIFY).
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run claims and executes jobs until ctx is done, then waits for running
// jobs to finish (their own contexts end with ctx).
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	sem := make(chan struct{}, r.slots)
	poll := time.NewTicker(r.poll)
	defer poll.Stop()
	reap := time.NewTicker(r.reapTick)
	defer reap.Stop()
	go r.listen(ctx)
	for {
		// Claim while slots are free.
		for {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			job, ok, err := r.Claim(ctx)
			if err != nil || !ok {
				<-sem
				if err != nil && ctx.Err() == nil {
					r.logger.Error("claim failed", slog.String("error", err.Error()))
				}
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				r.exec.Execute(ctx, job)
			}()
		}
		r.metrics.updateQueueDepth(ctx, r.pool)
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-poll.C:
		case <-reap.C:
			if err := r.Reap(ctx); err != nil && ctx.Err() == nil {
				r.logger.Error("reap failed", slog.String("error", err.Error()))
			}
		}
	}
}

// listen wakes the runner on NOTIFY replay_jobs. Polling covers lost
// notifications and reconnects, so failures here only add latency.
func (r *Runner) listen(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := r.listenDB.Acquire(ctx)
		if err != nil {
			sleep(ctx, time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err == nil {
			for {
				if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				r.Wake()
			}
		}
		// The connection may be mid-wait or listening; never return it to the pool.
		_ = conn.Conn().Close(context.WithoutCancel(ctx))
		conn.Release()
		sleep(ctx, time.Second)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
