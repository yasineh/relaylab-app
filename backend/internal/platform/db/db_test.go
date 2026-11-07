package db_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
