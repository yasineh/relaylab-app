// Package health implements liveness and readiness endpoints shared by the api
// and worker processes.
package health

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"relaylab/internal/platform/httpx"
)

// Timeout is the total budget for readiness checks.
const Timeout = 2 * time.Second

// Check reports whether one dependency is healthy. Errors are never exposed to
// clients; only "ok" or "fail" is returned.
type Check func(ctx context.Context) error

// Checker serves /healthz and /readyz.
type Checker struct {
	database     Check
	migrations   Check
	shuttingDown atomic.Bool
}

// New returns a Checker for the given dependency checks.
func New(database, migrations Check) *Checker {
	return &Checker{database: database, migrations: migrations}
}

// MarkShuttingDown makes readiness return 503 from now on.
func (c *Checker) MarkShuttingDown() { c.shuttingDown.Store(true) }

type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Liveness reports that the process can serve HTTP. It touches no dependency.
func (c *Checker) Liveness(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readiness checks the database and migrations within Timeout.
func (c *Checker) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), Timeout)
	defer cancel()

	checks := map[string]string{"database": "ok", "migrations": "ok"}
	ready := true
	if err := c.database(ctx); err != nil {
		checks["database"], checks["migrations"], ready = "fail", "fail", false
	} else if err := c.migrations(ctx); err != nil {
		checks["migrations"], ready = "fail", false
	}
	if c.shuttingDown.Load() {
		ready = false
	}
	if !ready {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, readyBody{Status: "not_ready", Checks: checks})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, readyBody{Status: "ready"})
}

// Register mounts the endpoints on mux-like routers.
func (c *Checker) Register(r interface {
	Get(pattern string, h http.HandlerFunc)
}) {
	r.Get("/healthz", c.Liveness)
	r.Get("/readyz", c.Readiness)
}
