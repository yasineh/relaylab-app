package retention

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// batchLog records the row limit of every expired-event purge statement.
type batchLog struct {
	mu     sync.Mutex
	limits []int32
}

func (l *batchLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "PurgeExpiredEvents") && len(d.Args) == 2 {
		if n, ok := d.Args[1].(int32); ok {
			l.mu.Lock()
			l.limits = append(l.limits, n)
			l.mu.Unlock()
		}
	}
	return ctx
}

func (l *batchLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// 2.7: a 50,000-event backlog goes in batches of at most 1,000 rows while
// ingestion-style inserts keep succeeding.
func TestLargeBacklogPurgedInBatchesWhileInsertsContinue(t *testing.T) {
	if testing.Short() {
		t.Skip("load-style test")
	}
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ep := e.endpoint(p, "E")
	e.events(p, ep, 50000, 8*day)

	var log batchLog
	cfg := e.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = &log
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	ctx, stop := context.WithCancel(context.Background())
	var inserted, failed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			ictx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := e.Pool.Exec(ictx, `
				INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params, headers,
				  body, body_size, body_encoding, correlation_id)
				VALUES ($1, $2, $3, now(), 'POST', '', '{}', '[]', '\x7b7d', 2, 'utf8', 'req')`,
				uuid.Must(uuid.NewV7()), ep, p)
			cancel()
			if err != nil {
				failed.Add(1)
			} else {
				inserted.Add(1)
			}
		}
	}()

	res, ran, err := New(pool, defaults(), nil, nil).RunOnce(context.Background())
	stop()
	wg.Wait()
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if res.Events != 50000 {
		t.Fatalf("deleted %d", res.Events)
	}
	if failed.Load() != 0 || inserted.Load() == 0 {
		t.Fatalf("concurrent inserts: %d ok, %d failed", inserted.Load(), failed.Load())
	}
	if len(log.limits) < 50 {
		t.Fatalf("only %d purge statements for 50,000 rows", len(log.limits))
	}
	for _, n := range log.limits {
		if n > 1000 {
			t.Fatalf("a statement deleted up to %d rows", n)
		}
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events`); n != int(inserted.Load()) {
		t.Fatalf("%d events left, %d inserted during the purge", n, inserted.Load())
	}
}
