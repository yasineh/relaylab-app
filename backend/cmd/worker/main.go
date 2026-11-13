// Command worker runs RelayLab background jobs and exposes liveness and
// readiness on an internal port (WORKER_HEALTH_ADDR, default :8081).
//
// It runs the retention purge (add-event-retention) every RETENTION_INTERVAL;
// a database advisory lock lets only one worker purge at a time. It runs
// replay jobs (add-event-replay, D5) from the Postgres queue: test-sink
// replays always, URL replays only when REPLAY_ENABLED is true, and every 15 seconds reports its outbound guard's
// self-check to app.worker_heartbeats, which the API reads before offering
// URL replay. Readiness reports the guard as outbound_guard (active or
// inactive), and /metrics on the same internal port exposes the outbound,
// replay, and retention metrics.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	"relaylab/internal/platform/redact"
	"relaylab/internal/replay"
	"relaylab/internal/replay/outbound"
	"relaylab/internal/retention"
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
	replayCfg, err := config.LoadReplay(os.Getenv, cfg.Env)
	if err != nil {
		logging.New(os.Stderr, "worker", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	ingestCfg, err := config.LoadIngest(os.Getenv)
	if err != nil {
		logging.New(os.Stderr, "worker", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	retentionCfg, err := config.LoadRetention(os.Getenv, cfg.Env)
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

	workerID := newWorkerID()
	go retention.New(pool, retentionCfg, logger, retention.NewMetrics(registry)).Run(ctx)
	go replay.NewHeartbeat(pool, workerID, outbound.UserAgent(), guard, logger).Run(ctx)
	// Replay jobs always run: test-sink replays never leave RelayLab and work
	// with REPLAY_ENABLED off; URL replays are refused unless it is on.
	{
		var sealer *replay.Sealer
		if key := replayCfg.OverrideKey.Reveal(); key != "" {
			if sealer, err = replay.NewSealer([]byte(key)); err != nil {
				logger.Error("invalid replay override key", slog.String("error", err.Error()))
				return 1
			}
		}
		replayMetrics := replay.NewMetrics(registry)
		deliverer := replay.NewDeliverer(replay.DelivererOptions{
			Pool: pool, Sender: guard, Sealer: sealer, Limits: replayCfg, Logger: logger, Metrics: replayMetrics,
			// The same redaction rules as ingestion, for sink events and response headers.
			Red: redact.New(ingestCfg.RedactExtraHeaders, ingestCfg.RedactExtraParams),
		})
		runner := replay.NewRunner(replay.RunnerOptions{
			Pool: pool, WorkerID: workerID, Limits: replayCfg, Slots: outCfg.MaxConcurrency,
			Executor: deliverer, Logger: logger, Metrics: replayMetrics,
		})
		deliverer.Bind(runner)
		go runner.Run(ctx)
	}
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
	logger.Info("worker started", slog.String("health_addr", ln.Addr().String()),
		slog.String("worker_id", workerID), slog.Bool("replay_enabled", replayCfg.Enabled),
		slog.String("retention_event_ttl", retentionCfg.EventTTL.String()), slog.Int("retention_max_events_per_project", retentionCfg.MaxEventsPerProject))

	if err := httpx.Serve(ctx, srv, ln, cfg.ShutdownGrace, checker.MarkShuttingDown); err != nil {
		logger.Error("worker health server stopped with error", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("worker stopped")
	return 0
}

// newWorkerID names this process for leases and heartbeats: host, pid, and a
// random suffix, so restarts never reuse an id.
func newWorkerID() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	id := fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))
	if len(id) > 128 {
		id = id[len(id)-128:]
	}
	return id
}
