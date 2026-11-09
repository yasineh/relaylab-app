package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// sqlRecorder captures the SQL and arguments the store actually sends, so the
// plan test explains the real query text rather than a copy of it.
type sqlRecorder struct {
	sql  string
	args []any
}

func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.sql, r.args = d.SQL, d.Args
	return ctx
}

func (r *sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// planNode is the subset of EXPLAIN (FORMAT JSON) output the test inspects.
type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	SharedHit    int        `json:"Shared Hit Blocks"`
	SharedRead   int        `json:"Shared Read Blocks"`
	ActualRows   int        `json:"Actual Rows"`
	Plans        []planNode `json:"Plans"`
}

func walk(n planNode, visit func(planNode)) {
	visit(n)
	for _, c := range n.Plans {
		walk(c, visit)
	}
}

// TestListQueriesUseIndexes checks that one page of the event list reads only
// a small part of webhook_events, for busy and quiet projects and endpoints.
//
// Postgres legitimately chooses between walking the primary key backwards
// (cheap when the project's events are recent) and the (project_id, id) or
// (endpoint_id, id) indexes, so the test asserts the work done, not the index
// name. It runs the queries the way the service does (newQuerier: always a
// custom plan) and also shows why the generic plan is avoided.
func TestListQueriesUseIndexes(t *testing.T) {
	e := newSeedEnv(t)
	ctx := context.Background()
	ep := e.endpoint(t, e.project, "Stripe")
	other := e.endpoint(t, e.project, "GitHub")
	// A shared deployment: 40000 events, 2000 of them in alice's project
	// (split across two endpoints) and the rest spread over 50 other
	// projects. A generic plan estimates "project_id = $1" from the number of
	// distinct projects, so a realistic spread matters: with only one or two
	// projects, scanning the primary key really would be cheaper.
	if _, err := e.Admin.Exec(ctx, `
		INSERT INTO app.projects (id, owner_id, name)
		SELECT ('00000000-0000-7000-8000-' || lpad(to_hex(g), 12, '0'))::uuid, 'bob', 'Noise ' || g
		FROM generate_series(1, 50) g;
		INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext)
		SELECT ('00000000-0000-7000-8000-' || lpad(to_hex(g), 12, '0'))::uuid,
		       ('00000000-0000-7000-8000-' || lpad(to_hex(g), 12, '0'))::uuid, 'Noise', sha256(g::text::bytea), '\x00'
		FROM generate_series(1, 50) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Admin.Exec(ctx, `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params,
		  headers, body, body_size, body_encoding, correlation_id)
		SELECT (lpad(to_hex((extract(epoch FROM now() - make_interval(secs => g)) * 1000)::bigint), 12, '0')
		        || '7' || substr(md5(g::text), 1, 3) || '8' || substr(md5(g::text), 4, 15))::uuid,
		  CASE WHEN g % 20 = 0 THEN $1::uuid WHEN g % 20 = 1 THEN $2::uuid ELSE n END,
		  CASE WHEN g % 20 IN (0, 1) THEN $3::uuid ELSE n END,
		  now() - make_interval(secs => g), 'POST', '', '{}', '[]', '', 0, 'utf8', 'seed'
		FROM generate_series(1, 40000) g,
		     LATERAL (SELECT ('00000000-0000-7000-8000-' || lpad(to_hex(g % 50 + 1), 12, '0'))::uuid AS n) x`,
		ep, other, e.project); err != nil {
		t.Fatal(err)
	}
	// A quiet project: 300 events, all older than every other event, so a
	// backward primary-key walk would read the whole table to find them.
	quiet := e.Project(t, "alice", "Quiet")
	quietEP := e.endpoint(t, quiet, "Quiet")
	if _, err := e.Admin.Exec(ctx, `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params,
		  headers, body, body_size, body_encoding, correlation_id)
		SELECT (lpad(to_hex((extract(epoch FROM now() - interval '30 days' - make_interval(secs => g)) * 1000)::bigint), 12, '0')
		        || '7' || substr(md5('q' || g), 1, 3) || '8' || substr(md5('q' || g), 4, 15))::uuid,
		  $1, $2, now() - interval '30 days' - make_interval(secs => g), 'POST', '', '{}', '[]', '', 0, 'utf8', 'seed'
		FROM generate_series(1, 300) g`, quietEP, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Admin.Exec(ctx, `ANALYZE app.webhook_events`); err != nil {
		t.Fatal(err)
	}
	var tablePages int
	if err := e.Admin.QueryRow(ctx, `SELECT relpages FROM pg_class WHERE oid = 'app.webhook_events'::regclass`).Scan(&tablePages); err != nil || tablePages < 100 {
		t.Fatalf("seed too small: %d pages, %v", tablePages, err)
	}

	cfg, err := pgx.ParseConfig(e.DB.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	rec := &sqlRecorder{}
	cfg.Tracer = rec
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	q := newQuerier(conn)

	cases := []struct {
		name    string
		project uuid.UUID
		f       Filter
	}{
		{"default feed", e.project, Filter{}},
		{"endpoint filter", e.project, Filter{EndpointID: &ep}},
		{"quiet project", quiet, Filter{}},
		{"quiet endpoint", quiet, Filter{EndpointID: &quietEP}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after, before := idWindow(tc.f, nil)
			if _, err := listRows(ctx, q, "alice", tc.project, tc.f, after, before, 51); err != nil {
				t.Fatal(err)
			}
			sql, args := rec.sql, rec.args
			plan, blocks := explainAnalyze(t, conn, sql, args...)
			assertNoSeqScan(t, plan)
			if blocks > tablePages/10 {
				t.Errorf("one page read %d blocks; webhook_events has %d; plan %s", blocks, tablePages, describe(plan))
			}
		})
	}

	// The service never leaves a named prepared statement behind, so Postgres
	// cannot switch these queries to a generic plan.
	res, err := conn.PgConn().Exec(ctx,
		`SELECT count(*) FROM pg_prepared_statements WHERE statement LIKE '%app.webhook_events%'`).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if n := string(res[0].Rows[0][0]); n != "0" {
		t.Errorf("%s prepared statements on webhook_events, want 0", n)
	}
	t.Run("generic plan is the reason", func(t *testing.T) {
		after, before := idWindow(Filter{}, nil)
		if _, err := listRows(ctx, q, "alice", e.project, Filter{}, after, before, 51); err != nil {
			t.Fatal(err)
		}
		if !hasNode(explainGeneric(t, conn, rec.sql), "Sort") {
			t.Skip("the generic plan no longer sorts the whole project; customPlanDB may be unnecessary")
		}
	})
}

// explainAnalyze runs sql with args and returns its plan and the shared
// buffers it touched.
func explainAnalyze(t *testing.T, conn *pgx.Conn, sql string, args ...any) (planNode, int) {
	t.Helper()
	var raw []byte
	if err := conn.QueryRow(context.Background(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql,
		append([]any{pgx.QueryExecModeExec}, args...)...).Scan(&raw); err != nil {
		t.Fatalf("explain: %v", err)
	}
	p := parsePlan(t, raw)
	return p, p.SharedHit + p.SharedRead
}

// explainGeneric plans sql with its $n parameters unbound, as a cached
// generic plan would run. pgx refuses to send placeholders without
// arguments and the extended protocol binds them, so this uses a simple query.
func explainGeneric(t *testing.T, conn *pgx.Conn, sql string) planNode {
	t.Helper()
	res, err := conn.PgConn().Exec(context.Background(), "EXPLAIN (GENERIC_PLAN, FORMAT JSON) "+sql).ReadAll()
	if err != nil || len(res) != 1 || len(res[0].Rows) != 1 {
		t.Fatalf("generic plan: %v", err)
	}
	return parsePlan(t, res[0].Rows[0][0])
}

func parsePlan(t *testing.T, raw []byte) planNode {
	t.Helper()
	var out []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out) != 1 {
		t.Fatalf("parse plan: %v", err)
	}
	return out[0].Plan
}

func assertNoSeqScan(t *testing.T, plan planNode) {
	t.Helper()
	walk(plan, func(n planNode) {
		if n.RelationName == "webhook_events" && n.NodeType == "Seq Scan" {
			t.Error("sequential scan on webhook_events")
		}
	})
}

func hasNode(plan planNode, nodeType string) bool {
	found := false
	walk(plan, func(n planNode) { found = found || n.NodeType == nodeType })
	return found
}

// describe renders a plan tree on one line for failure messages.
func describe(plan planNode) string {
	var parts []string
	walk(plan, func(n planNode) {
		parts = append(parts, fmt.Sprintf("%s(%s %s rows=%d hit=%d)", n.NodeType, n.RelationName, n.IndexName, n.ActualRows, n.SharedHit))
	})
	return strings.Join(parts, " ")
}
