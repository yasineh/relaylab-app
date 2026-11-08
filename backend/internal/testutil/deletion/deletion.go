// Package deletion enforces the permanent-deletion contract (D18) in tests:
// deleting a project, or its owner, must leave no row behind in any
// project-scoped table, and every project_id column must cascade.
//
// Every change that adds a project-scoped table registers it in tables.go in
// the same pull request.
package deletion

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Table is a project-scoped table and a SQL predicate that selects its rows
// for one project, with the project id as $1. The predicate must not join
// through other project-scoped tables (they are deleted too); use a
// project_id column on the table itself.
type Table struct {
	Name  string // schema-qualified, e.g. app.endpoints
	Where string // e.g. "project_id = $1"
}

var (
	mu       sync.Mutex
	registry = map[string]Table{}
)

// Register adds a project-scoped table. It panics on duplicates so two changes
// cannot silently register the same table with different predicates.
func Register(name, where string) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic("deletion: table registered twice: " + name)
	}
	registry[name] = Table{Name: name, Where: where}
}

// Tables returns the registered tables sorted by name.
func Tables() []Table {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Table, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Querier is satisfied by *pgx.Conn, pgxpool.Pool, and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// AssertNoOrphans fails the test if any registered table still has rows for projectID.
func AssertNoOrphans(t testing.TB, q Querier, projectID any) {
	t.Helper()
	for _, tbl := range Tables() {
		var n int
		// Table names and predicates come from the registry in this package, never from input.
		sql := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", tbl.Name, tbl.Where)
		if err := q.QueryRow(context.Background(), sql, projectID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl.Name, err)
		}
		if n != 0 {
			t.Errorf("%d orphaned row(s) in %s for project %v", n, tbl.Name, projectID)
		}
	}
}

// UncascadedProjectColumns lists app tables that have a project_id column
// without a foreign key to app.projects(id) ON DELETE CASCADE on that column.
func UncascadedProjectColumns(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT c.table_schema || '.' || c.table_name
		FROM information_schema.columns c
		WHERE c.table_schema = 'app' AND c.column_name = 'project_id'
		  AND NOT EXISTS (
		    SELECT 1
		    FROM pg_constraint con
		    JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY (con.conkey)
		    WHERE con.contype = 'f'
		      AND con.conrelid = (quote_ident(c.table_schema) || '.' || quote_ident(c.table_name))::regclass
		      AND con.confrelid = 'app.projects'::regclass
		      AND con.confdeltype = 'c'
		      AND a.attname = 'project_id'
		  )
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
