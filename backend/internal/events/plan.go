package events

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"relaylab/internal/platform/db/store"
)

// customPlanDB runs every query as an unnamed statement (pgx
// QueryExecModeExec), so Postgres plans it with the request's actual values
// each time. pgx's default statement cache would let Postgres switch to a
// generic plan after a few executions, and for the event list that plan reads
// every event in the project and sorts it on each page (see
// TestListQueriesUseIndexes). Planning these queries is cheap; reading a
// whole project per page is not.
type customPlanDB struct{ db store.DBTX }

// newQuerier returns the store the explorer uses.
func newQuerier(db store.DBTX) *store.Queries { return store.New(customPlanDB{db}) }

func withMode(args []any) []any { return append([]any{pgx.QueryExecModeExec}, args...) }

func (c customPlanDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return c.db.Exec(ctx, sql, withMode(args)...)
}

func (c customPlanDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return c.db.Query(ctx, sql, withMode(args)...)
}

func (c customPlanDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.db.QueryRow(ctx, sql, withMode(args)...)
}
