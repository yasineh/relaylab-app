// Package retention deletes stored data once it is past its retention period
// (add-event-retention, D10). The worker runs one Purger; a session-level
// advisory lock makes sure only one worker purges at a time.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
)

const (
	// batchRows caps the rows one DELETE statement removes, so ingestion and
	// dashboard queries are never blocked behind a long delete.
	batchRows = 1000
	// runBudget bounds one purge run; whatever is left waits for the next.
	runBudget = 2 * time.Minute
)

// Result counts what one run deleted.
type Result struct {
	Events, Replays, Audit, Jobs, OrphanedJobs int64
}

// Purger runs the retention purge every Interval.
type Purger struct {
	pool    *pgxpool.Pool
	cfg     *config.Retention
	logger  *slog.Logger
	metrics *Metrics
	batch   int32
	budget  time.Duration
	demoCap int // DEMO_MAX_EVENTS per demo project; 0: no separate cap
}

// WithDemoCap sets the stored-event cap for demo users' projects.
func (p *Purger) WithDemoCap(n int) *Purger {
	p.demoCap = n
	return p
}

// New returns a purger. metrics may be nil.
func New(pool *pgxpool.Pool, cfg *config.Retention, logger *slog.Logger, metrics *Metrics) *Purger {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Purger{pool: pool, cfg: cfg, logger: logger, metrics: metrics, batch: batchRows, budget: runBudget}
}

// Run purges once at start and then every Interval until ctx ends.
func (p *Purger) Run(ctx context.Context) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		if _, _, err := p.RunOnce(ctx); err != nil && ctx.Err() == nil {
			p.logger.Error("retention purge failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce performs one purge if no other worker holds the retention lock.
// ran is false when another worker is purging. Each batch commits on its own,
// so an error keeps the progress made and the next run continues from there.
func (p *Purger) RunOnce(ctx context.Context) (res Result, ran bool, err error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return res, false, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()
	q := store.New(conn)
	got, err := q.TryRetentionLock(ctx)
	if err != nil {
		return res, false, fmt.Errorf("retention lock: %w", err)
	}
	if !got {
		p.logger.Debug("retention purge skipped; another worker holds the lock")
		return res, false, nil
	}
	defer func() {
		// The lock is per session: release it before the connection returns to the pool.
		if _, uerr := q.ReleaseRetentionLock(context.WithoutCancel(ctx)); uerr != nil {
			conn.Conn().Close(context.WithoutCancel(ctx)) //nolint:errcheck,gosec // closing ends the session and its lock
		}
	}()

	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, p.budget)
	defer cancel()
	var errs []error
	step := func(name string, f func() error) {
		if runCtx.Err() != nil {
			return
		}
		if err := f(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	step("expired events", func() error { return p.purgeExpired(runCtx, q, &res) })
	step("event cap", func() error { return p.purgeOverCap(runCtx, q, &res) })
	if p.demoCap > 0 {
		step("demo event cap", func() error { return p.purgeDemoOverCap(runCtx, q, &res) })
	}
	step("audit log", func() error {
		return p.loop(runCtx, func() (int64, error) {
			n, err := q.PurgeAuditLog(runCtx, store.PurgeAuditLogParams{TtlSeconds: p.cfg.AuditTTL.Seconds(), RowLimit: p.batch})
			res.Audit += n
			return n, err
		})
	})
	step("finished jobs", func() error {
		return p.loop(runCtx, func() (int64, error) {
			n, err := q.PurgeFinishedJobs(runCtx, store.PurgeFinishedJobsParams{TtlSeconds: p.cfg.JobTTL.Seconds(), RowLimit: p.batch})
			res.Jobs += n
			return n, err
		})
	})
	step("orphaned jobs", func() error {
		return p.loop(runCtx, func() (int64, error) {
			n, err := q.PurgeOrphanedJobs(runCtx, p.batch)
			res.OrphanedJobs += n
			return n, err
		})
	})

	d := time.Since(start)
	p.metrics.record(res, d)
	attrs := []any{
		slog.Int64("events", res.Events), slog.Int64("replay_attempts", res.Replays),
		slog.Int64("audit_log", res.Audit), slog.Int64("jobs", res.Jobs),
		slog.Int64("jobs_orphaned", res.OrphanedJobs), slog.Int64("duration_ms", d.Milliseconds()),
		slog.Bool("budget_exhausted", runCtx.Err() != nil && ctx.Err() == nil),
	}
	p.logger.Info("retention purge finished", attrs...)
	return res, true, errors.Join(errs...)
}

// loop runs batch until it deletes fewer than a full batch.
func (p *Purger) loop(ctx context.Context, batch func() (int64, error)) error {
	for ctx.Err() == nil {
		n, err := batch()
		if err != nil {
			return err
		}
		if n < int64(p.batch) {
			return nil
		}
	}
	return ctx.Err()
}

func (p *Purger) purgeExpired(ctx context.Context, q *store.Queries, res *Result) error {
	if p.cfg.EventTTL <= 0 {
		return nil // disabled (development only; production rejects 0)
	}
	return p.loop(ctx, func() (int64, error) {
		r, err := q.PurgeExpiredEvents(ctx, store.PurgeExpiredEventsParams{TtlSeconds: p.cfg.EventTTL.Seconds(), RowLimit: p.batch})
		res.Events += r.Events
		res.Replays += r.Replays
		return r.Events, err
	})
}

func (p *Purger) purgeOverCap(ctx context.Context, q *store.Queries, res *Result) error {
	limit := int64(p.cfg.MaxEventsPerProject)
	projects, err := q.ProjectsOverEventCap(ctx, limit)
	if err != nil {
		return err
	}
	return p.capProjects(ctx, q, res, projects, limit)
}

func (p *Purger) purgeDemoOverCap(ctx context.Context, q *store.Queries, res *Result) error {
	limit := int64(p.demoCap)
	projects, err := q.DemoProjectsOverEventCap(ctx, limit)
	if err != nil {
		return err
	}
	return p.capProjects(ctx, q, res, projects, limit)
}

// capProjects deletes each project's oldest events beyond limit.
func (p *Purger) capProjects(ctx context.Context, q *store.Queries, res *Result, projects []pgtype.UUID, limit int64) error {
	for _, project := range projects {
		b, err := q.EventCapBoundary(ctx, store.EventCapBoundaryParams{ProjectID: project, Cap: limit})
		if errors.Is(err, pgx.ErrNoRows) {
			continue // back under the cap since the count (manual deletion)
		}
		if err != nil {
			return err
		}
		if err := p.loop(ctx, func() (int64, error) {
			r, err := q.PurgeProjectEventsThrough(ctx, store.PurgeProjectEventsThroughParams{
				ProjectID: project, ReceivedAt: b.ReceivedAt, ID: b.ID, RowLimit: p.batch,
			})
			res.Events += r.Events
			res.Replays += r.Replays
			return r.Events, err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Metrics are the retention collectors. The table label is a fixed set.
type Metrics struct {
	deleted  *prometheus.CounterVec
	duration prometheus.Histogram
}

// NewMetrics registers the retention collectors on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		deleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "retention_deleted_total",
			Help: "Rows deleted by the retention purge, by table.",
		}, []string{"table"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "retention_run_duration_seconds",
			Help:    "Duration of one retention purge run.",
			Buckets: prometheus.ExponentialBuckets(0.01, 3, 9), // 10 ms .. ~66 s
		}),
	}
	reg.MustRegister(m.deleted, m.duration)
	for _, t := range []string{"webhook_events", "replay_attempts", "audit_log", "jobs"} {
		m.deleted.WithLabelValues(t)
	}
	return m
}

func (m *Metrics) record(r Result, d time.Duration) {
	if m == nil {
		return
	}
	m.deleted.WithLabelValues("webhook_events").Add(float64(r.Events))
	m.deleted.WithLabelValues("replay_attempts").Add(float64(r.Replays))
	m.deleted.WithLabelValues("audit_log").Add(float64(r.Audit))
	m.deleted.WithLabelValues("jobs").Add(float64(r.Jobs + r.OrphanedJobs))
	m.duration.Observe(d.Seconds())
}
