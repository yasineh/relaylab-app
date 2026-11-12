// Command worker runs RelayLab background jobs and exposes liveness and
// readiness on an internal port (WORKER_HEALTH_ADDR, default :8081).
//
// No job handlers exist yet; job processing arrives with add-event-replay (D5).
// The outbound request guard used by replay is built and self-checked at
// startup; readiness reports it as outbound_guard (active or inactive), and
// /metrics on the same internal port exposes outbound_requests_total.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
	"relaylab/internal/replay/outbound"
)

func main() { os.Exit(run()) }

func run() int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logging.New(os.Stderr, "worker", os.Getenv("APP_ENV"), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	outCfg, err := config.LoadOutbound(os.Getenv, cfg)
	if err != nil {
		logging.New(os.Stderr, "worker", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	logger := logging.New(os.Stdout, "worker", string(cfg.Env), cfg.LogLevel)
	slog.SetDefault(logger)

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	guard := outbound.New(outbound.OptionsFrom(outCfg, outbound.UserAgent(), logger, outbound.NewMetrics(registry)))
	logger.Info("outbound guard", slog.Bool("active", guard.Active()), slog.Bool("allow_http", outCfg.AllowHTTP))

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
	checker.AddFlag("outbound_guard", func() string {
		if guard.Active() {
			return "active"
		}
		return "inactive"
	})

	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware(logger), httpx.Recoverer)
	r.NotFound(httpx.NotFound)
	checker.Register(r)
	// The health port is internal (never exposed publicly), like the API's METRICS_ADDR.
	r.Method(http.MethodGet, "/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
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
