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
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

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

// Migrate runs the real `auth migrate` command against d as auth_migrator: the
// Better Auth tables plus the grants to app_runtime and app_migrator.
func Migrate(t testing.TB, d *dbtest.DB) {
	t.Helper()
	dir := Require(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "src/cli/migrate.ts")
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), "AUTH_MIGRATION_DATABASE_URL="+d.AuthMigratorURL, "LOG_LEVEL=warn")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("auth migrate: %v\n%s", err, out)
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
