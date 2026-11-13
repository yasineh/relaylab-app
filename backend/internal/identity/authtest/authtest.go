// Package authtest runs the real auth service (auth/, Better Auth) from Go
// integration and contract tests: its migrations and, optionally, the server.
//
// It needs Node.js and the auth package's dependencies (pnpm install). Tests
// skip when they are missing, unless RELAYLAB_REQUIRE_AUTH=1 (the CI
// auth-contract job), where they fail instead.
package authtest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/db/dbtest"
)

// Test-only shared secrets for the auth service and the Go validator/proxy.
//
//nolint:gosec // G101: intentional, non-secret test values.
const (
	SessionSecret = "test-only-better-auth-secret-0123456789abcdef"
	ProxySecret   = "test-only-proxy-shared-secret-0123456789abcdef"
	AppOrigin     = "http://relaylab.test"
)

// Require skips (or fails, when RELAYLAB_REQUIRE_AUTH=1) unless Node.js and the
// auth package's dependencies are available. It returns the auth package dir.
func Require(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(dbtest.RepoRoot(), "auth")
	missing := ""
	if _, err := exec.LookPath("node"); err != nil {
		missing = "node is not on PATH"
	} else if _, err := os.Stat(filepath.Join(dir, "node_modules", "better-auth")); err != nil {
		missing = "auth dependencies are not installed (run pnpm install)"
	}
	if missing != "" {
		if os.Getenv("RELAYLAB_REQUIRE_AUTH") == "1" {
			t.Fatalf("auth service unavailable: %s (RELAYLAB_REQUIRE_AUTH=1)", missing)
		}
		t.Skipf("auth service unavailable: %s", missing)
	}
	return dir
}

// Prepare gives d an auth schema so app migrations (which reference
// auth."user") can run: the real auth migrations when Node.js and the auth
// package are available, otherwise dbtest.StubAuthSchema. With
// RELAYLAB_REQUIRE_AUTH=1 only the real migrations are accepted.
func Prepare(t testing.TB, d *dbtest.DB) {
	t.Helper()
	if available() || os.Getenv("RELAYLAB_REQUIRE_AUTH") == "1" {
		Migrate(t, d)
		return
	}
	if err := dbtest.StubAuthSchema(context.Background(), d); err != nil {
		t.Fatalf("stub auth schema: %v", err)
	}
}

func available() bool {
	if _, err := exec.LookPath("node"); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dbtest.RepoRoot(), "auth", "node_modules", "better-auth"))
	return err == nil
}

// Migrate runs the real `auth migrate` command against d as auth_migrator: the
// Better Auth tables plus the grants to app_runtime and app_migrator.
func Migrate(t testing.TB, d *dbtest.DB) {
	t.Helper()
	dir := Require(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The migrate command GRANTs to shared roles: serialise with other packages' role setup.
	err := dbtest.WithRoleLock(ctx, func() error {
		cmd := exec.CommandContext(ctx, "node", "src/cli/migrate.ts")
		cmd.Dir = dir
		cmd.Env = append(baseEnv(), "AUTH_MIGRATION_DATABASE_URL="+d.AuthMigratorURL, "LOG_LEVEL=warn")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w\n%s", err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("auth migrate: %v", err)
	}
}

// Service is a running auth service process.
type Service struct {
	URL string // e.g. http://127.0.0.1:41234

	mu   sync.Mutex
	logs bytes.Buffer
}

// Logs returns everything the service has written so far.
func (s *Service) Logs() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logs.String()
}

type lockedWriter struct{ s *Service }

func (w lockedWriter) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	return w.s.logs.Write(p)
}

// Start boots the auth service on a free port against d (already migrated),
// waits until it is ready, and stops it when the test ends. extraEnv entries
// (KEY=VALUE) override the defaults.
func Start(t testing.TB, d *dbtest.DB, extraEnv ...string) *Service {
	t.Helper()
	dir := Require(t)
	port := freePort(t)
	svc := &Service{URL: "http://127.0.0.1:" + strconv.Itoa(port)}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "node", "src/main.ts")
	cmd.Dir = dir
	cmd.Env = append(baseEnv(),
		"APP_ENV=test",
		"APP_ORIGIN="+AppOrigin,
		"AUTH_PORT="+strconv.Itoa(port),
		"AUTH_DATABASE_URL="+d.AuthRuntimeURL,
		"BETTER_AUTH_SECRET="+SessionSecret,
		"AUTH_PROXY_SHARED_SECRET="+ProxySecret,
		"AUTH_SIGNUP_ENABLED=true",
		"AUTH_REQUIRE_EMAIL_VERIFICATION=true",
		"AUTH_RATE_LIMIT_ENABLED=false",
		"LOG_LEVEL=info",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdout = lockedWriter{svc}
	cmd.Stderr = lockedWriter{svc}
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start auth service: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, svc.URL+"/readyz", nil)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return svc
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("auth service did not become ready:\n%s", svc.Logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// baseEnv passes through only what Node needs; no developer secrets leak in.
func baseEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "NODE_ENV=production"}
	if v := os.Getenv("TMPDIR"); v != "" {
		env = append(env, "TMPDIR="+v)
	}
	return env
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal(fmt.Errorf("unexpected address type %T", l.Addr()))
	}
	return addr.Port
}

// SessionCookie inserts a session for userID (who must exist in auth."user")
// expiring in a day, and returns a Cookie header value signed with
// SessionSecret, in Better Auth's format: relaylab.session_token=<token>.<sig>.
func SessionCookie(t testing.TB, d *dbtest.DB, userID string) string {
	t.Helper()
	cookie, _ := Session(t, d, userID, time.Now().Add(24*time.Hour))
	return cookie
}

// Session inserts a session for userID expiring at expiresAt and returns its
// Cookie header value and its token (the auth."session".token column, for
// tests that revoke or extend it).
func Session(t testing.TB, d *dbtest.DB, userID string, expiresAt time.Time) (cookie, token string) {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	token = hex.EncodeToString(b)[:32]
	ctx := context.Background()
	c, err := pgx.Connect(ctx, d.AuthRuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, `INSERT INTO auth."session" (id, "expiresAt", token, "createdAt", "updatedAt", "userId")
		VALUES ($1, $2, $3, now(), now(), $4)`, "s-"+token, expiresAt, token, userID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(SessionSecret))
	mac.Write([]byte(token))
	value := token + "." + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return "relaylab.session_token=" + url.QueryEscape(value), token
}

// SeedUser inserts a verified user with the given id (email <id>@example.test).
func SeedUser(t testing.TB, d *dbtest.DB, userID string) {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, d.AuthRuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ($1, $1, $1 || '@example.test', true, now(), now())`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// SeedDemoUser inserts a demo user (a Better Auth anonymous user) created at
// createdAt, as the auth service's anonymous sign-in would.
func SeedDemoUser(t testing.TB, d *dbtest.DB, userID string, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, d.AuthRuntimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, `INSERT INTO auth."user" (id, name, email, "emailVerified", "createdAt", "updatedAt", "isAnonymous")
		VALUES ($1, 'Demo visitor', 'temp-' || $1 || '@demo.relaylab.invalid', false, $2, $2, true)`, userID, createdAt); err != nil {
		t.Fatalf("seed demo user: %v", err)
	}
}
