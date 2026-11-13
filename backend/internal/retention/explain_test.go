package retention

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

type planNode struct {
	NodeType  string     `json:"Node Type"`
	IndexName string     `json:"Index Name"`
	Plans     []planNode `json:"Plans"`
}

func indexesIn(n planNode, into map[string]bool) {
	if n.IndexName != "" {
		into[n.IndexName] = true
	}
	for _, c := range n.Plans {
		indexesIn(c, into)
	}
}

func (e *env) planIndexes(sql string, args ...any) map[string]bool {
	e.t.Helper()
	var raw []byte
	if err := e.Admin.QueryRow(context.Background(), "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
		e.t.Fatal(err)
	}
	var out []struct{ Plan planNode }
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatal(err)
	}
	got := map[string]bool{}
	indexesIn(out[0].Plan, got)
	return got
}

// 1.2: the purge statements use the retention indexes rather than scanning
// webhook_events, audit_log, or jobs.
func TestPurgeQueriesUseIndexes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var target uuid.UUID
	for i := range 20 {
		p := e.Project(t, "alice", "P")
		ep := e.endpoint(p, "E")
		e.events(p, ep, 1000, time.Hour)
		if i == 0 {
			target = p
			e.events(p, ep, 20, 8*day)
		}
	}
	if _, err := e.Admin.Exec(ctx, `ANALYZE app.webhook_events; ANALYZE app.replay_attempts; ANALYZE app.audit_log; ANALYZE app.jobs`); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		sql   string
		args  []any
		index string
	}{
		"expired events": {
			`SELECT id FROM app.webhook_events WHERE received_at < now() - make_interval(secs => $1::float8) ORDER BY received_at LIMIT 1000`,
			[]any{(7 * day).Seconds()}, "webhook_events_received_idx",
		},
		"cap boundary": {
			`SELECT received_at, id FROM app.webhook_events WHERE project_id = $1 ORDER BY received_at DESC, id DESC OFFSET 500 LIMIT 1`,
			[]any{target}, "webhook_events_project_received_id_idx",
		},
		"cap batch": {
			`SELECT e.id FROM app.webhook_events e WHERE e.project_id = $1 AND (e.received_at, e.id) <= (now(), $2::uuid)
			 ORDER BY e.received_at, e.id LIMIT 1000`,
			[]any{target, uuid.Max}, "webhook_events_project_received_id_idx",
		},
		"sink reference clearing": {
			`SELECT id FROM app.webhook_events WHERE replay_attempt_id = $1`,
			[]any{uuid.Nil}, "webhook_events_replay_attempt_idx",
		},
	} {
		if got := e.planIndexes(c.sql, c.args...); !got[c.index] {
			t.Errorf("%s: plan uses %v, want %s", name, got, c.index)
		}
	}
	// audit_log and jobs are empty here; with sequential scans disabled the
	// planner must still have an index to choose.
	tx, err := e.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	for sql, index := range map[string]string{
		`SELECT id FROM app.audit_log WHERE created_at < now() - interval '90 days' ORDER BY created_at LIMIT 1000`:                         "audit_log_created_idx",
		`SELECT id FROM app.jobs WHERE state IN ('done', 'dead') AND updated_at < now() - interval '7 days' ORDER BY updated_at LIMIT 1000`: "jobs_finished_updated_idx",
	} {
		var raw []byte
		if err := tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var out []struct{ Plan planNode }
		_ = json.Unmarshal(raw, &out)
		got := map[string]bool{}
		indexesIn(out[0].Plan, got)
		if !got[index] {
			t.Errorf("%s: plan uses %v, want %s", sql, got, index)
		}
	}
}
