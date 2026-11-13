package retention

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/testutil/fixture"
)

const day = 24 * time.Hour

func defaults() *config.Retention {
	return &config.Retention{EventTTL: 7 * day, MaxEventsPerProject: 10000, AuditTTL: 90 * day, JobTTL: 7 * day, Interval: time.Minute}
}

type env struct {
	*fixture.Env
	t *testing.T
}

func newEnv(t *testing.T) *env {
	return &env{Env: fixture.New(t, "alice", "bob"), t: t}
}

func (e *env) endpoint(project uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := e.Admin.Exec(context.Background(),
		`INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext) VALUES ($1::uuid, $2, $3, sha256($1::uuid::text::bytea), '\x00')`,
		id, project, name); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// events inserts n events received age ago (spaced a millisecond apart, the
// newest at age) and returns their ids, oldest first.
func (e *env) events(project, endpoint uuid.UUID, n int, age time.Duration) []uuid.UUID {
	e.t.Helper()
	rows, err := e.Admin.Query(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params, headers,
		  body, body_size, body_encoding, correlation_id)
		SELECT gen_random_uuid(), $1, $2, now() - make_interval(secs => $3::float8) - (($4 - g) * interval '1 millisecond'),
		       'POST', '', '{}', '[]', '\x7b7d', 2, 'utf8', 'req'
		FROM generate_series(1, $4::int) g
		RETURNING id, received_at`, endpoint, project, age.Seconds(), n)
	if err != nil {
		e.t.Fatal(err)
	}
	type row struct {
		id uuid.UUID
		at time.Time
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.at); err != nil {
			e.t.Fatal(err)
		}
		all = append(all, r)
	}
	if rows.Err() != nil {
		e.t.Fatal(rows.Err())
	}
	ids := make([]uuid.UUID, len(all))
	for i, r := range all {
		ids[i] = r.id
	}
	return ids
}

// replays adds n finished sink replays of event.
func (e *env) replays(project, event uuid.UUID, n int) {
	e.t.Helper()
	for range n {
		if _, err := e.Admin.Exec(context.Background(), `
			INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, method, state, outcome)
			VALUES ($1, $2, $3, 'sink', 'POST', 'succeeded', 'succeeded')`, uuid.Must(uuid.NewV7()), project, event); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) exists(table string, id uuid.UUID) bool {
	return e.Count(e.t, `SELECT count(*) FROM app.`+table+` WHERE id = $1`, id) == 1
}

func (e *env) purger(cfg *config.Retention) *Purger {
	return New(e.Pool, cfg, nil, nil)
}

func (e *env) runOnce(p *Purger) Result {
	e.t.Helper()
	res, ran, err := p.RunOnce(context.Background())
	if err != nil || !ran {
		e.t.Fatalf("run: ran=%v err=%v", ran, err)
	}
	return res
}

// 2.1: only one worker purges at a time.
func TestOnlyOneWorkerPurges(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ep := e.endpoint(p, "E")
	e.events(p, ep, 5, 8*day)

	// Another worker is mid-run: it holds the retention lock.
	ctx := context.Background()
	if _, err := e.Admin.Exec(ctx, `SELECT pg_advisory_lock(727101)`); err != nil {
		t.Fatal(err)
	}
	res, ran, err := e.purger(defaults()).RunOnce(ctx)
	if err != nil || ran || res.Events != 0 {
		t.Fatalf("ran while locked: ran=%v res=%+v err=%v", ran, res, err)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events`); n != 5 {
		t.Fatalf("skipped run deleted rows: %d left", n)
	}
	if _, err := e.Admin.Exec(ctx, `SELECT pg_advisory_unlock(727101)`); err != nil {
		t.Fatal(err)
	}

	// Two workers at the same moment: together they delete each row once,
	// and every run that reports ran held the lock alone.
	var wg sync.WaitGroup
	results := make([]Result, 2)
	rans := make([]bool, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], rans[i], _ = e.purger(defaults()).RunOnce(ctx)
		}()
	}
	wg.Wait()
	if !rans[0] && !rans[1] {
		t.Fatal("neither worker purged")
	}
	if results[0].Events+results[1].Events != 5 {
		t.Fatalf("deleted %d + %d events", results[0].Events, results[1].Events)
	}
	// The lock is released after each run.
	var free bool
	if err := e.Admin.QueryRow(ctx, `SELECT pg_try_advisory_lock(727101)`).Scan(&free); err != nil || !free {
		t.Fatalf("lock still held after runs: %v %v", free, err)
	}
	_, _ = e.Admin.Exec(ctx, `SELECT pg_advisory_unlock(727101)`)
}

// 2.2: expired events go with their replays; recent events stay.
func TestExpiredEventsPurgedWithReplays(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ep := e.endpoint(p, "E")
	expired := e.events(p, ep, 1, 7*day+time.Hour)[0]
	recent := e.events(p, ep, 1, 6*day)[0]
	e.replays(p, expired, 3)
	e.replays(p, recent, 1)

	// A sink event created by one of the expired event's replays stays, with
	// its replay reference cleared.
	sink := e.events(p, ep, 1, time.Hour)[0]
	var replayID uuid.UUID
	if err := e.Admin.QueryRow(context.Background(),
		`SELECT id FROM app.replay_attempts WHERE event_id = $1 LIMIT 1`, expired).Scan(&replayID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Admin.Exec(context.Background(),
		`UPDATE app.webhook_events SET source = 'replay', replay_attempt_id = $1 WHERE id = $2`, replayID, sink); err != nil {
		t.Fatal(err)
	}

	res := e.runOnce(e.purger(defaults()))
	if res.Events != 1 || res.Replays != 3 {
		t.Fatalf("result %+v", res)
	}
	if e.exists("webhook_events", expired) || !e.exists("webhook_events", recent) || !e.exists("webhook_events", sink) {
		t.Fatal("wrong events deleted")
	}
	if n := e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE event_id = $1`, expired); n != 0 {
		t.Fatalf("%d replays of the expired event remain", n)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE event_id = $1`, recent); n != 1 {
		t.Fatalf("replay of the recent event deleted")
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE id = $1 AND replay_attempt_id IS NULL`, sink); n != 1 {
		t.Fatal("sink event's replay reference not cleared")
	}
}

func TestZeroTTLDisablesTimePurge(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	e.events(p, e.endpoint(p, "E"), 2, 30*day)
	cfg := defaults()
	cfg.EventTTL = 0
	if res := e.runOnce(e.purger(cfg)); res.Events != 0 {
		t.Fatalf("deleted %d with TTL 0", res.Events)
	}
}

// 2.3: the cap keeps the newest events of a busy project only.
func TestEventCapPerProject(t *testing.T) {
	e := newEnv(t)
	a := e.Project(t, "alice", "A")
	b := e.Project(t, "bob", "B")
	aIDs := e.events(a, e.endpoint(a, "E"), 10050, time.Hour)
	e.events(b, e.endpoint(b, "E"), 100, 2*time.Hour)

	res := e.runOnce(e.purger(defaults()))
	if res.Events != 50 {
		t.Fatalf("deleted %d, want 50", res.Events)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE project_id = $1`, a); n != 10000 {
		t.Fatalf("project A has %d", n)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE project_id = $1`, b); n != 100 {
		t.Fatalf("project B has %d", n)
	}
	// The 50 oldest went; the 51st oldest stayed.
	for _, id := range aIDs[:50] {
		if e.exists("webhook_events", id) {
			t.Fatalf("old event %s kept", id)
		}
	}
	if !e.exists("webhook_events", aIDs[50]) {
		t.Fatal("event inside the cap deleted")
	}
}

func TestEventCapBreaksTiesByID(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ep := e.endpoint(p, "E")
	ids := e.events(p, ep, 5, time.Hour)
	if _, err := e.Admin.Exec(context.Background(),
		`UPDATE app.webhook_events SET received_at = date_trunc('second', now()) WHERE project_id = $1`, p); err != nil {
		t.Fatal(err)
	}
	cfg := defaults()
	cfg.MaxEventsPerProject = 3
	if res := e.runOnce(e.purger(cfg)); res.Events != 2 {
		t.Fatalf("deleted %d with equal timestamps", res.Events)
	}
	var kept []uuid.UUID
	rows, _ := e.Admin.Query(context.Background(), `SELECT id FROM app.webhook_events ORDER BY id`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		kept = append(kept, id)
	}
	maxDeleted := uuid.Nil
	for _, id := range ids {
		if !e.exists("webhook_events", id) && bytes.Compare(id[:], maxDeleted[:]) > 0 {
			maxDeleted = id
		}
	}
	for _, id := range kept {
		if bytes.Compare(id[:], maxDeleted[:]) < 0 {
			t.Fatalf("kept %s but deleted the larger id %s", id, maxDeleted)
		}
	}
}

// 2.4: audit and finished jobs expire; pending jobs never do; auth untouched.
func TestAuditAndFinishedJobsPurged(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ctx := context.Background()
	oldAudit, newAudit := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, a := range []struct {
		id  uuid.UUID
		age time.Duration
	}{{oldAudit, 91 * day}, {newAudit, 89 * day}} {
		if _, err := e.Admin.Exec(ctx, `INSERT INTO app.audit_log (id, project_id, action, resource_type, created_at)
			VALUES ($1, $2, 'replay.created', 'replay', now() - make_interval(secs => $3::float8))`, a.id, p, a.age.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	jobs := map[string]uuid.UUID{}
	for _, j := range []struct {
		name, state string
		age         time.Duration
	}{
		{"done-old", "done", 8 * day}, {"dead-old", "dead", 8 * day}, {"done-new", "done", 6 * day},
		{"queued-old", "queued", 400 * day}, {"running-old", "running", 400 * day},
	} {
		id := uuid.Must(uuid.NewV7())
		jobs[j.name] = id
		lease := "NULL"
		if j.state == "running" {
			lease = "'w'"
		}
		// A queued or running job keeps a live replay so the orphan sweep leaves it.
		ev := e.events(p, e.endpoint(p, j.name), 1, time.Hour)[0]
		replay := uuid.Must(uuid.NewV7())
		if _, err := e.Admin.Exec(ctx, `INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, method)
			VALUES ($1, $2, $3, 'sink', 'POST')`, replay, p, ev); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]string{"replay_id": replay.String()})
		if _, err := e.Admin.Exec(ctx, `INSERT INTO app.jobs (id, kind, project_id, payload, state, max_attempts, lease_owner,
			lease_expires_at, updated_at)
			VALUES ($1, 'replay.deliver', $2, $3, $4, 3, `+lease+`, CASE WHEN `+lease+` IS NULL THEN NULL ELSE now() END,
			  now() - make_interval(secs => $5::float8))`, id, p, payload, j.state, j.age.Seconds()); err != nil {
			t.Fatal(err)
		}
	}

	res := e.runOnce(e.purger(defaults()))
	if res.Audit != 1 || res.Jobs != 2 || res.OrphanedJobs != 0 {
		t.Fatalf("result %+v", res)
	}
	if e.exists("audit_log", oldAudit) || !e.exists("audit_log", newAudit) {
		t.Fatal("wrong audit entries deleted")
	}
	for name, want := range map[string]bool{"done-old": false, "dead-old": false, "done-new": true, "queued-old": true, "running-old": true} {
		if e.exists("jobs", jobs[name]) != want {
			t.Errorf("job %s exists=%v, want %v", name, !want, want)
		}
	}
}

func TestWorkerRoleCannotWriteAuthSchema(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	rows, err := e.Pool.Query(ctx, `
		SELECT c.relname,
		       has_table_privilege(c.oid, 'INSERT') OR has_table_privilege(c.oid, 'UPDATE')
		       OR has_table_privilege(c.oid, 'DELETE') OR has_table_privilege(c.oid, 'TRUNCATE')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'auth' AND c.relkind = 'r'`)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for rows.Next() {
		var name string
		var write bool
		if err := rows.Scan(&name, &write); err != nil {
			t.Fatal(err)
		}
		seen++
		if write {
			t.Errorf("worker role can write auth.%s", name)
		}
	}
	if seen == 0 {
		t.Fatal("no auth tables found")
	}
	if _, err := e.Pool.Exec(ctx, `DELETE FROM auth.session`); err == nil {
		t.Fatal("worker role deleted from auth.session")
	}
}

// The purge statements never name the auth schema.
func TestPurgeQueriesAvoidAuthSchema(t *testing.T) {
	var rec sqlLog
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	e.events(p, e.endpoint(p, "E"), 3, 8*day)
	cfg := e.Pool.Config().Copy()
	cfg.ConnConfig.Tracer = &rec
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, ran, err := New(pool, defaults(), nil, nil).RunOnce(context.Background()); err != nil || !ran {
		t.Fatal(ran, err)
	}
	if len(rec.all()) < 5 {
		t.Fatalf("recorded only %d statements", len(rec.all()))
	}
	for _, sql := range rec.all() {
		if strings.Contains(strings.ToLower(sql), "auth.") {
			t.Fatalf("purge touched the auth schema: %s", sql)
		}
	}
}

// 2.5: orphaned jobs are swept; running ones are left to their worker.
func TestOrphanedJobs(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ctx := context.Background()
	ep := e.endpoint(p, "E")
	job := func(state string, replay uuid.UUID) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		payload, _ := json.Marshal(map[string]string{"replay_id": replay.String()})
		lease := "NULL"
		if state == "running" {
			lease = "'w'"
		}
		if _, err := e.Admin.Exec(ctx, `INSERT INTO app.jobs (id, kind, project_id, payload, state, max_attempts, lease_owner, lease_expires_at)
			VALUES ($1, 'replay.deliver', $2, $3, $4, 3, `+lease+`, CASE WHEN `+lease+` IS NULL THEN NULL ELSE now() END)`,
			id, p, payload, state); err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := e.events(p, ep, 1, time.Hour)[0]
	liveReplay := uuid.Must(uuid.NewV7())
	if _, err := e.Admin.Exec(ctx, `INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, method)
		VALUES ($1, $2, $3, 'sink', 'POST')`, liveReplay, p, live); err != nil {
		t.Fatal(err)
	}
	gone := uuid.Must(uuid.NewV7()) // its event was deleted, and the replay with it
	queuedOrphan := job("queued", gone)
	runningOrphan := job("running", gone)
	healthy := job("queued", liveReplay)
	if _, err := e.Admin.Exec(ctx, `INSERT INTO app.jobs (id, kind, project_id, payload, max_attempts)
		VALUES ($1, 'replay.deliver', $2, '{"replay_id": "not-a-uuid"}', 3)`, uuid.Must(uuid.NewV7()), p); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	pr := New(e.Pool, defaults(), slog.New(slog.NewJSONHandler(&logs, nil)), nil)
	res := e.runOnce(pr)
	if res.OrphanedJobs != 2 { // the queued orphan and the malformed payload
		t.Fatalf("result %+v", res)
	}
	if e.exists("jobs", queuedOrphan) || !e.exists("jobs", runningOrphan) || !e.exists("jobs", healthy) {
		t.Fatal("wrong jobs swept")
	}
	if !strings.Contains(logs.String(), `"jobs_orphaned":2`) {
		t.Fatalf("log lacks the orphan count: %s", logs.String())
	}
}

// 2.6: logs and metrics carry counts, never payload data.
func TestPurgeLogsAndMetrics(t *testing.T) {
	e := newEnv(t)
	p := e.Project(t, "alice", "P")
	ep := e.endpoint(p, "E")
	ids := e.events(p, ep, 2, 8*day)
	e.replays(p, ids[0], 1)
	if _, err := e.Admin.Exec(context.Background(),
		`UPDATE app.webhook_events SET body = convert_to('{"card":"4242-secret"}', 'UTF8'), body_size = 22,
		  path = '/secret-path', raw_query = 'token=sekrit' WHERE project_id = $1`, p); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	reg := prometheus.NewRegistry()
	pr := New(e.Pool, defaults(), slog.New(slog.NewJSONHandler(&logs, nil)), NewMetrics(reg))
	e.runOnce(pr)

	out := logs.String()
	for _, want := range []string{`"msg":"retention purge finished"`, `"events":2`, `"replay_attempts":1`, `"duration_ms":`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
	for _, leak := range []string{"4242", "secret", "sekrit", ids[0].String(), p.String()} {
		if strings.Contains(out, leak) {
			t.Errorf("log contains %q", leak)
		}
	}
	m, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(pr.metrics.deleted.WithLabelValues("webhook_events")); got != 2 {
		t.Fatalf("retention_deleted_total{table=webhook_events} = %v", got)
	}
	if got := testutil.ToFloat64(pr.metrics.deleted.WithLabelValues("replay_attempts")); got != 1 {
		t.Fatalf("retention_deleted_total{table=replay_attempts} = %v", got)
	}
	names := map[string]bool{}
	for _, f := range m {
		names[f.GetName()] = true
	}
	if !names["retention_deleted_total"] || !names["retention_run_duration_seconds"] {
		t.Fatalf("metrics registered: %v", names)
	}
}

// sqlLog records every statement a pool sends.
type sqlLog struct {
	mu   sync.Mutex
	sqls []string
}

func (l *sqlLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	l.sqls = append(l.sqls, d.SQL)
	l.mu.Unlock()
	return ctx
}

func (l *sqlLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (l *sqlLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.sqls...)
}

// Demo projects are capped at DEMO_MAX_EVENTS; real projects keep the normal cap.
func TestDemoProjectEventCap(t *testing.T) {
	e := newEnv(t)
	authtest.SeedDemoUser(t, e.DB, "demo-cap", time.Now())
	demoProject := e.Project(t, "demo-cap", "Demo project")
	realProject := e.Project(t, "alice", "Real")
	e.events(demoProject, e.endpoint(demoProject, "D"), 210, time.Hour)
	e.events(realProject, e.endpoint(realProject, "R"), 210, time.Hour)
	res := e.runOnce(e.purger(defaults()).WithDemoCap(200))
	if res.Events != 10 {
		t.Fatalf("deleted %d", res.Events)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE project_id = $1`, demoProject); n != 200 {
		t.Fatalf("demo project has %d", n)
	}
	if n := e.Count(t, `SELECT count(*) FROM app.webhook_events WHERE project_id = $1`, realProject); n != 210 {
		t.Fatalf("real project has %d", n)
	}
}
