// Package dbtest provisions throwaway PostgreSQL databases for integration tests.
//
// Set RELAYLAB_TEST_DATABASE_URL to a superuser URL (for the local compose
// stack: postgres://postgres:dev-only-postgres@localhost:5432/postgres). Tests
// skip when it is unset, unless RELAYLAB_REQUIRE_DB=1 (CI), where they fail.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Test-only role passwords. Roles are cluster-wide; these are applied to the
// ephemeral test cluster only and never used elsewhere.
//
//nolint:gosec // G101: intentional, non-secret credentials for the throwaway test database.
const (
	MigratorPassword     = "test-only-app-migrator"
	RuntimePassword      = "test-only-app-runtime"
	AuthMigratorPassword = "test-only-auth-migrator"
	AuthRuntimePassword  = "test-only-auth-runtime"
)

// DB describes a freshly created database with RelayLab schemas and roles.
type DB struct {
	Name        string
	AdminURL    string // superuser connection to this database
	MigratorURL string // app_migrator
	RuntimeURL  string // app_runtime

	AuthMigratorURL string // auth_migrator (Better Auth migrations)
	AuthRuntimeURL  string // auth_runtime (auth service)
}

// New creates an isolated database, applies deploy/sql/roles.sql, and drops it
// when the test finishes.
func New(t testing.TB) *DB {
	t.Helper()
	base := os.Getenv("RELAYLAB_TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("RELAYLAB_REQUIRE_DB") == "1" {
			t.Fatal("RELAYLAB_TEST_DATABASE_URL is required (RELAYLAB_REQUIRE_DB=1)")
		}
		t.Skip("RELAYLAB_TEST_DATABASE_URL not set; skipping database integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to test cluster: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	name := "relaylab_test_" + randHex(6)
	// CREATE/DROP DATABASE also touch shared catalogs (pg_database, pg_shdepend),
	// so they run under the role lock like roles.sql and grants.
	if err := WithRoleLock(ctx, func() error {
		_, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
		return err
	}); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer func() { _ = c.Close(ctx) }()
		if err := WithRoleLock(ctx, func() error {
			_, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			return err
		}); err != nil {
			t.Logf("cleanup drop: %v", err)
		}
	})

	d := &DB{
		Name:        name,
		AdminURL:    withDB(t, base, name, "", ""),
		MigratorURL: withDB(t, base, name, "app_migrator", MigratorPassword),
		RuntimeURL:  withDB(t, base, name, "app_runtime", RuntimePassword),

		AuthMigratorURL: withDB(t, base, name, "auth_migrator", AuthMigratorPassword),
		AuthRuntimeURL:  withDB(t, base, name, "auth_runtime", AuthRuntimePassword),
	}

	// Roles are cluster-wide: serialise role DDL across parallel test packages.
	if err := WithRoleLock(ctx, func() error { return retryConcurrentUpdate(func() error { return ApplyRoles(ctx, d.AdminURL) }) }); err != nil {
		t.Fatalf("apply roles.sql: %v", err)
	}
	return d
}

// ApplyRoles runs deploy/sql/roles.sql against the database at adminURL.
func ApplyRoles(ctx context.Context, adminURL string) error {
	script, err := os.ReadFile(RolesScriptPath())
	if err != nil {
		return err
	}
	c, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(ctx) }()
	settings := fmt.Sprintf(
		"SET relaylab.app_migrator_password = '%s'; SET relaylab.app_runtime_password = '%s'; "+
			"SET relaylab.auth_migrator_password = '%s'; SET relaylab.auth_runtime_password = '%s';",
		MigratorPassword, RuntimePassword, AuthMigratorPassword, AuthRuntimePassword)
	if _, err := c.Exec(ctx, settings); err != nil {
		return err
	}
	_, err = c.Exec(ctx, string(script))
	return err
}

// RepoRoot returns the repository root (the directory containing deploy/sql/roles.sql).
func RepoRoot() string {
	return filepath.Dir(filepath.Dir(filepath.Dir(RolesScriptPath())))
}

// RolesScriptPath locates deploy/sql/roles.sql by walking up from the working
// directory (tests run inside backend/, the script lives at the repo root).
func RolesScriptPath() string {
	rel := filepath.Join("deploy", "sql", "roles.sql")
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return rel
		}
		dir = parent
	}
}

func withDB(t testing.TB, base, dbName, user, pass string) string {
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse RELAYLAB_TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + dbName
	if user != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u.String()
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StubAuthSchema creates the two Better Auth tables the Go side depends on
// (auth."user" and auth."session", exactly as declared for sqlc in
// db/sqlc/10_auth_tables.sql) plus the grants the real auth migrate command
// applies. It stands in for the auth migrations where Node.js is unavailable;
// prefer authtest.Prepare, which uses the real migrations when it can.
func StubAuthSchema(ctx context.Context, d *DB) error {
	return WithRoleLock(ctx, func() error { return stubAuthSchema(ctx, d) })
}

// WithRoleLock runs fn while holding the cluster-wide advisory lock that
// serialises role and grant changes across concurrently running test packages
// (and the auth service's tests, which use the same key). GRANTs touch shared
// catalogs, so concurrent role DDL fails with "tuple concurrently updated".
func WithRoleLock(ctx context.Context, fn func() error) error {
	base := os.Getenv("RELAYLAB_TEST_DATABASE_URL")
	c, err := pgx.Connect(ctx, base)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, "SELECT pg_advisory_lock(727001)"); err != nil {
		return err
	}
	defer func() { _, _ = c.Exec(context.Background(), "SELECT pg_advisory_unlock(727001)") }()
	return fn()
}

// concurrentUpdateAttempts bounds retryConcurrentUpdate.
const concurrentUpdateAttempts = 5

// retryConcurrentUpdate retries fn when PostgreSQL reports "tuple concurrently
// updated" (XX000), which shared-catalog DDL can still hit when another
// cluster client (outside these helpers) changes the same role at once.
func retryConcurrentUpdate(fn func() error) error {
	var err error
	for attempt := range concurrentUpdateAttempts {
		if err = fn(); err == nil || !strings.Contains(err.Error(), "tuple concurrently updated") {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	return err
}

func stubAuthSchema(ctx context.Context, d *DB) error {
	ddl, err := os.ReadFile(filepath.Join(RepoRoot(), "backend", "db", "sqlc", "10_auth_tables.sql"))
	if err != nil {
		return err
	}
	c, err := pgx.Connect(ctx, d.AuthMigratorURL)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(ctx) }()
	for _, stmt := range []string{
		string(ddl),
		`GRANT SELECT ON auth."session", auth."user" TO app_runtime`,
		`GRANT REFERENCES ON auth."user" TO app_migrator`,
	} {
		if _, err := c.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
