package db_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
)

var quiet = slog.New(slog.DiscardHandler)

func TestRuntimeRoleCannotRunDDL(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	for _, stmt := range []string{
		"CREATE TABLE app.x (id int)",
		"CREATE TABLE auth.x (id int)",
		"CREATE TABLE public.x (id int)",
		"CREATE SCHEMA evil",
	} {
		_, err := conn.Exec(ctx, stmt)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" { // insufficient_privilege
			t.Errorf("%q: expected permission error, got %v", stmt, err)
		}
	}
}

func TestSchemasAndOwners(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	owners := map[string]string{}
	rows, err := conn.Query(ctx, `SELECT n.nspname, r.rolname FROM pg_namespace n
	  JOIN pg_roles r ON r.oid = n.nspowner WHERE n.nspname IN ('app','auth')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s, o string
		if err := rows.Scan(&s, &o); err != nil {
			t.Fatal(err)
		}
		owners[s] = o
	}
	if owners["app"] != "app_migrator" || owners["auth"] != "auth_migrator" {
		t.Fatalf("unexpected schema owners: %v", owners)
	}

	var authTables int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'auth'`).Scan(&authTables); err != nil {
		t.Fatal(err)
	}
	if authTables != 0 {
		t.Fatalf("auth schema should be empty, has %d tables", authTables)
	}
}

func TestRolesScriptIsIdempotent(t *testing.T) {
	d := dbtest.New(t)
	if err := dbtest.ApplyRoles(context.Background(), d.AdminURL); err != nil {
		t.Fatalf("second run of roles.sql failed: %v", err)
	}
}

func TestMigrateUpDownUp(t *testing.T) {
	d := dbtest.New(t)
	authtest.Prepare(t, d) // app tables reference auth."user"
	ctx := context.Background()
	migrator := config.NewSecret(d.MigratorURL)

	pool, err := db.Open(ctx, config.NewSecret(d.RuntimeURL))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	latest, err := db.LatestVersion()
	if err != nil || latest < 1 {
		t.Fatalf("latest=%d err=%v", latest, err)
	}

	if v, err := db.AppliedVersion(ctx, pool); err != nil || v != 0 {
		t.Fatalf("before migrate: applied=%d err=%v", v, err)
	}
	if err := db.Migrate(ctx, migrator, quiet); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v, err := db.AppliedVersion(ctx, pool); err != nil || v != latest {
		t.Fatalf("after up: applied=%d latest=%d err=%v", v, latest, err)
	}
	if err := db.MigrateDownAll(ctx, migrator, quiet); err != nil {
		t.Fatalf("down: %v", err)
	}
	if v, err := db.AppliedVersion(ctx, pool); err != nil || v != 0 {
		t.Fatalf("after down: applied=%d err=%v", v, err)
	}
	if err := db.Migrate(ctx, migrator, quiet); err != nil {
		t.Fatalf("up again: %v", err)
	}

	// goose bookkeeping lives in the app schema, created by the migrator role.
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	var owner string
	if err := admin.QueryRow(ctx, `SELECT tableowner FROM pg_tables
	  WHERE schemaname = 'app' AND tablename = 'goose_db_version'`).Scan(&owner); err != nil {
		t.Fatalf("goose table not in app schema: %v", err)
	}
	if owner != "app_migrator" {
		t.Fatalf("goose table owner = %s", owner)
	}
}

func TestRuntimeRoleCannotMigrate(t *testing.T) {
	d := dbtest.New(t)
	err := db.Migrate(context.Background(), config.NewSecret(d.RuntimeURL), quiet)
	if err == nil {
		t.Fatal("runtime role must not be able to apply migrations")
	}
}

// The runtime role reads goose's version table (readiness reports the applied
// version) but must not change it: a compromised API could otherwise make a
// later deploy skip or repeat migrations (release-hardening-review).
func TestRuntimeRoleCannotRewriteMigrationHistory(t *testing.T) {
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), quiet); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	for priv, want := range map[string]bool{"SELECT": true, "INSERT": false, "UPDATE": false, "DELETE": false, "TRUNCATE": false} {
		var got bool
		if err := admin.QueryRow(ctx, `SELECT has_table_privilege('app_runtime', 'app.goose_db_version', $1)`, priv).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("app_runtime %s on app.goose_db_version = %v, want %v", priv, got, want)
		}
	}
	runtime, err := db.Open(ctx, config.NewSecret(d.RuntimeURL))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := db.AppliedVersion(ctx, runtime); err != nil {
		t.Fatalf("runtime role must still read the applied version: %v", err)
	}
}

func TestMigrateFailsFastWithoutAuthSchema(t *testing.T) {
	d := dbtest.New(t)
	err := db.Migrate(context.Background(), config.NewSecret(d.MigratorURL), quiet)
	if err == nil {
		t.Fatal("app migrations must refuse to run before the auth migrations")
	}
	if !strings.Contains(err.Error(), "run the auth migrations") {
		t.Fatalf("error does not explain the fix: %v", err)
	}
}

func TestDeletingAuthUserDeletesTheirProjects(t *testing.T) {
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), quiet); err != nil {
		t.Fatal(err)
	}
	authConn, err := pgx.Connect(ctx, d.AuthRuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = authConn.Close(ctx) }()
	appConn, err := pgx.Connect(ctx, d.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = appConn.Close(ctx) }()

	for _, u := range []string{"keep", "gone"} {
		if _, err := authConn.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
			VALUES ($1, $1, $1 || '@example.test', true, now(), now())`, u); err != nil {
			t.Fatal(err)
		}
		if _, err := appConn.Exec(ctx, `INSERT INTO app.projects (id, owner_id, name) VALUES (gen_random_uuid(), $1, 'p')`, u); err != nil {
			t.Fatal(err)
		}
	}
	// The auth service deletes users as auth_runtime; the FK cascade removes their projects.
	if _, err := authConn.Exec(ctx, `DELETE FROM auth."user" WHERE id = 'gone'`); err != nil {
		t.Fatal(err)
	}
	var gone, kept int
	if err := appConn.QueryRow(ctx, `SELECT count(*) FROM app.projects WHERE owner_id = 'gone'`).Scan(&gone); err != nil {
		t.Fatalf("count deleted user's projects: %v", err)
	}
	if err := appConn.QueryRow(ctx, `SELECT count(*) FROM app.projects WHERE owner_id = 'keep'`).Scan(&kept); err != nil {
		t.Fatalf("count kept user's projects: %v", err)
	}
	if gone != 0 || kept != 1 {
		t.Fatalf("after deleting user: gone=%d kept=%d", gone, kept)
	}
}

// TestWebhookEventConstraints checks the webhook_events integrity rules: the
// denormalized project_id must match the endpoint's project, body_size must
// match the stored bytes, and deleting an endpoint removes its events.
func TestWebhookEventConstraints(t *testing.T) {
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), quiet); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	app, err := pgx.Connect(ctx, d.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(ctx) }()

	if _, err := admin.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ('u1', 'U', 'u1@example.test', true, now(), now())`); err != nil {
		t.Fatal(err)
	}
	var p1, p2, e1 string
	for _, p := range []*string{&p1, &p2} {
		if err := app.QueryRow(ctx, `INSERT INTO app.projects (id, owner_id, name) VALUES (gen_random_uuid(), 'u1', 'p') RETURNING id::text`).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.QueryRow(ctx, `INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext)
		VALUES (gen_random_uuid(), $1, 'e', sha256('t'::bytea), '\x00') RETURNING id::text`, p1).Scan(&e1); err != nil {
		t.Fatal(err)
	}

	insert := func(project string, body []byte, size int) error {
		_, err := app.Exec(ctx, `INSERT INTO app.webhook_events
			(id, endpoint_id, project_id, received_at, method, path, query_params, headers, body, body_size, body_encoding, correlation_id)
			VALUES (gen_random_uuid(), $1, $2, now(), 'POST', '', '{}', '[]', $3, $4, 'binary', 'req-1')`,
			e1, project, body, size)
		return err
	}
	wantViolation := func(name string, err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("%s: want SQLSTATE %s, got %v", name, code, err)
		}
	}

	if err := insert(p1, []byte{0xff, 0xfe, 0x00}, 3); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	wantViolation("event in another project", insert(p2, []byte{}, 0), "23503")
	wantViolation("body_size mismatch", insert(p1, []byte("abc"), 2), "23514")

	// Deleting the endpoint removes its events.
	if _, err := app.Exec(ctx, `DELETE FROM app.endpoints WHERE id = $1`, e1); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM app.webhook_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("events left after endpoint delete: %d", n)
	}
}

func TestReplayConstraints(t *testing.T) {
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), quiet); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	app, err := pgx.Connect(ctx, d.RuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(ctx) }()
	if _, err := admin.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ('u1', 'U', 'u1@example.test', true, now(), now())`); err != nil {
		t.Fatal(err)
	}
	one := func(sql string, args ...any) string {
		t.Helper()
		var id string
		if err := app.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	project := func() string {
		return one(`INSERT INTO app.projects (id, owner_id, name) VALUES (gen_random_uuid(), 'u1', 'p') RETURNING id::text`)
	}
	endpoint := func(p string) string {
		return one(`INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext)
			VALUES (gen_random_uuid(), $1, 'e', sha256(gen_random_uuid()::text::bytea), '\x00') RETURNING id::text`, p)
	}
	event := func(p, e string) string {
		return one(`INSERT INTO app.webhook_events
			(id, endpoint_id, project_id, received_at, method, path, query_params, headers, body, body_size, body_encoding, correlation_id)
			VALUES (gen_random_uuid(), $1, $2, now(), 'POST', '', '{}', '[]', '\x', 0, 'utf8', 'r') RETURNING id::text`, e, p)
	}
	replay := func(p, ev, kind string, target, sink any) error {
		_, err := app.Exec(ctx, `INSERT INTO app.replay_attempts (id, project_id, event_id, actor_user_id, destination_type, target_url, sink_endpoint_id, method)
			VALUES (gen_random_uuid(), $1, $2, 'u1', $3, $4, $5, 'POST')`, p, ev, kind, target, sink)
		return err
	}
	want := func(name string, err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("%s: want SQLSTATE %s, got %v", name, code, err)
		}
	}

	p1, p2 := project(), project()
	e1, e2 := endpoint(p1), endpoint(p2)
	sink := endpoint(p1)
	ev1 := event(p1, e1)
	if err := replay(p1, ev1, "url", "https://example.com/", nil); err != nil {
		t.Fatalf("url replay rejected: %v", err)
	}
	if err := replay(p1, ev1, "sink", nil, sink); err != nil {
		t.Fatalf("sink replay rejected: %v", err)
	}
	want("event of another project", replay(p2, ev1, "url", "https://example.com/", nil), "23503")
	want("sink of another project", replay(p1, ev1, "sink", nil, e2), "23503")
	want("url without target", replay(p1, ev1, "url", nil, nil), "23514")
	want("sink with target", replay(p1, ev1, "sink", "https://example.com/", sink), "23514")
	_, err = app.Exec(ctx, `UPDATE app.replay_attempts SET state = 'succeeded', outcome = 'succeeded', header_overrides_ciphertext = '\x01'`)
	want("terminal with ciphertext", err, "23514")
	_, err = app.Exec(ctx, `UPDATE app.replay_attempts SET state = 'failed'`)
	want("terminal without outcome", err, "23514")
	_, err = app.Exec(ctx, `INSERT INTO app.jobs (id, kind, project_id, state, max_attempts) VALUES (gen_random_uuid(), 'replay.deliver', $1, 'running', 3)`, p1)
	want("running job without lease", err, "23514")

	// Deleting the sink endpoint keeps the replay and its project.
	if _, err := app.Exec(ctx, `DELETE FROM app.endpoints WHERE id = $1`, sink); err != nil {
		t.Fatal(err)
	}
	var kept int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM app.replay_attempts WHERE destination_type = 'sink' AND project_id = $1 AND sink_endpoint_id IS NULL`, p1).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("sink replay after endpoint deletion: %d %v", kept, err)
	}

	// Deleting the project removes its replays and jobs, including running ones.
	if _, err := app.Exec(ctx, `INSERT INTO app.jobs (id, kind, project_id, state, max_attempts, lease_owner, lease_expires_at)
		VALUES (gen_random_uuid(), 'replay.deliver', $1, 'running', 3, 'w1', now() + interval '1 minute'),
		       (gen_random_uuid(), 'replay.deliver', $1, 'queued', 3, NULL, NULL)`, p1); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(ctx, `DELETE FROM app.projects WHERE id = $1`, p1); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := app.QueryRow(ctx, `SELECT (SELECT count(*) FROM app.jobs) + (SELECT count(*) FROM app.replay_attempts)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows left after project deletion: %d %v", left, err)
	}
}
