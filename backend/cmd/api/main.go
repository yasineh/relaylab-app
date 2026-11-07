// Command api runs the RelayLab HTTP server: dashboard API, public ingestion,
// static web app, and health endpoints.
//
//	api           start the HTTP server (connects as app_runtime)
//	api migrate   apply database migrations (connects as app_migrator)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			os.Exit(runMigrate())
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (expected: migrate)\n", os.Args[1])
			os.Exit(2)
		}
	}
	os.Exit(runServer())
}

func runMigrate() int {
	logger := logging.New(os.Stdout, "migrate", os.Getenv("APP_ENV"), slog.LevelInfo)
	dsn, err := config.LoadMigrationURL(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := db.Migrate(ctx, dsn, logger); err != nil {
		logger.Error("migration failed", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("migrations applied")
	return 0
}

func runServer() int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		// FieldError messages name variables only, never values.
		logging.New(os.Stderr, "api", os.Getenv("APP_ENV"), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	logger := logging.New(os.Stdout, "api", string(cfg.Env), cfg.LogLevel)
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

	handler := newRouter(routerDeps{cfg: cfg, logger: logger, health: checker, web: webHandler()})
	srv := httpx.NewServer(cfg.HTTPAddr, handler, cfg.Limits.HTTPMaxHeaderBytes, logger)

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		logger.Error("listen failed", slog.String("addr", cfg.HTTPAddr), slog.String("error", err.Error()))
		return 1
	}
	logger.Info("api listening", slog.String("addr", ln.Addr().String()))
	if err := httpx.Serve(ctx, srv, ln, cfg.ShutdownGrace, checker.MarkShuttingDown); err != nil {
		logger.Error("server stopped with error", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("api stopped")
	return 0
}
