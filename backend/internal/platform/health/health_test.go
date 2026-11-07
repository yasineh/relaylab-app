package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/platform/health"
)

func ok(context.Context) error { return nil }

func get(t *testing.T, c *health.Checker, path string) (int, map[string]any, string) {
	t.Helper()
	r := chi.NewRouter()
	c.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %q", rec.Body)
	}
	return rec.Code, body, rec.Body.String()
}

func TestLivenessIgnoresDependencies(t *testing.T) {
	down := func(context.Context) error { return errors.New("dial tcp 10.0.0.1: password=hunter2") }
	code, body, raw := get(t, health.New(down, down), "/healthz")
	if code != 200 || body["status"] != "ok" || len(body) != 1 {
		t.Fatalf("got %d %s", code, raw)
	}
}

func TestReadinessHealthy(t *testing.T) {
	code, body, raw := get(t, health.New(ok, ok), "/readyz")
	if code != 200 || body["status"] != "ready" {
		t.Fatalf("got %d %s", code, raw)
	}
}

func TestReadinessDatabaseDownHidesErrors(t *testing.T) {
	down := func(context.Context) error { return errors.New("dial tcp 10.0.0.1: password=hunter2") }
	code, body, raw := get(t, health.New(down, ok), "/readyz")
	checks, _ := body["checks"].(map[string]any)
	if code != 503 || body["status"] != "not_ready" || checks["database"] != "fail" {
		t.Fatalf("got %d %s", code, raw)
	}
	if strings.Contains(raw, "hunter2") || strings.Contains(raw, "dial") {
		t.Fatalf("error details leaked: %s", raw)
	}
}

func TestReadinessPendingMigrations(t *testing.T) {
	pending := func(context.Context) error { return health.ErrPendingMigrations }
	code, body, raw := get(t, health.New(ok, pending), "/readyz")
	checks, _ := body["checks"].(map[string]any)
	if code != 503 || checks["migrations"] != "fail" || checks["database"] != "ok" {
		t.Fatalf("got %d %s", code, raw)
	}
}

func TestReadinessRespectsTimeout(t *testing.T) {
	slow := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	start := time.Now()
	code, _, _ := get(t, health.New(slow, ok), "/readyz")
	if code != 503 || time.Since(start) > health.Timeout+time.Second {
		t.Fatalf("code=%d took %v", code, time.Since(start))
	}
}

func TestReadinessFlipsOnShutdown(t *testing.T) {
	c := health.New(ok, ok)
	c.MarkShuttingDown()
	if code, _, raw := get(t, c, "/readyz"); code != 503 {
		t.Fatalf("got %d %s", code, raw)
	}
	if code, _, _ := get(t, c, "/healthz"); code != 200 {
		t.Fatal("liveness should stay up during shutdown")
	}
}

// Integration: real Postgres checks for pending and applied migrations.
func TestPostgresChecks(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	pool, err := db.Open(ctx, config.NewSecret(d.RuntimeURL))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dbCheck, migCheck, err := health.PostgresChecks(pool)
	if err != nil {
		t.Fatal(err)
	}
	c := health.New(dbCheck, migCheck)

	if code, body, raw := get(t, c, "/readyz"); code != 503 || body["checks"].(map[string]any)["migrations"] != "fail" {
		t.Fatalf("before migrate: %d %s", code, raw)
	}
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if code, _, raw := get(t, c, "/readyz"); code != http.StatusOK {
		t.Fatalf("after migrate: %d %s", code, raw)
	}
}
