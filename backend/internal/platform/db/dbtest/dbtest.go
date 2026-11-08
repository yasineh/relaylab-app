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
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
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
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
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
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock(727001)"); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "SELECT pg_advisory_unlock(727001)") }()

	if err := ApplyRoles(ctx, d.AdminURL); err != nil {
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
