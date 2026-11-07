// Command worker runs RelayLab background jobs and exposes liveness and
// readiness on an internal port (WORKER_HEALTH_ADDR, default :8081).
//
// No job handlers exist yet; job processing arrives with add-event-replay (D5).
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

func main() { os.Exit(run()) }

func run() int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logging.New(os.Stderr, "worker", os.Getenv("APP_ENV"), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	logger := logging.New(os.Stdout, "worker", string(cfg.Env), cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database setup failed", slog.String("error", err.Error()))
		return 1
	}
	defer pool.Close()

	dbCheck, migCheck, err := health.PostgresChecks(pool)
	if err != nil {
		logger.Error("health setup failed", slog.String("error", err.Error()))
		return 1
	}
	checker := health.New(dbCheck, migCheck)

	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware(logger), httpx.Recoverer)
	r.NotFound(httpx.NotFound)
	checker.Register(r)
	srv := httpx.NewServer(cfg.WorkerHealthAddr, r, cfg.Limits.HTTPMaxHeaderBytes, logger)

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.WorkerHealthAddr)
	if err != nil {
		logger.Error("listen failed", slog.String("addr", cfg.WorkerHealthAddr), slog.String("error", err.Error()))
		return 1
	}
	logger.Info("worker started; no job handlers registered yet", slog.String("health_addr", ln.Addr().String()))

	if err := httpx.Serve(ctx, srv, ln, cfg.ShutdownGrace, checker.MarkShuttingDown); err != nil {
		logger.Error("worker health server stopped with error", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("worker stopped")
	return 0
}
