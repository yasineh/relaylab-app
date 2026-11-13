// Command api runs the RelayLab HTTP server: dashboard API, public ingestion,
// static web app, and health endpoints.
//
//	api                    start the HTTP server (connects as app_runtime)
//	api migrate            apply database migrations (connects as app_migrator)
//	api healthcheck [url]  exit 0 if url (default http://127.0.0.1:8080/healthz)
//	                       answers 200; the image's HEALTHCHECK, since the
//	                       distroless runtime has no shell or curl
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"relaylab/internal/endpoints"
	"relaylab/internal/endpoints/token"
	"relaylab/internal/events"
	"relaylab/internal/identity"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
	"relaylab/internal/platform/redact"
	"relaylab/internal/projects"
	"relaylab/internal/realtime"
	"relaylab/internal/replay"
	"relaylab/internal/replay/outbound"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			os.Exit(runMigrate())
		case "healthcheck":
			os.Exit(runHealthcheck(os.Args[2:]))
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (expected: migrate, healthcheck)\n", os.Args[1])
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
	authCfg, err := config.LoadAuth(os.Getenv, cfg.Env)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	endpointCfg, err := config.LoadEndpoints(os.Getenv, cfg.Env, cfg.AppOrigin)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	ingestCfg, err := config.LoadIngest(os.Getenv)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	replayCfg, err := config.LoadReplay(os.Getenv, cfg.Env)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	retentionCfg, err := config.LoadRetention(os.Getenv, cfg.Env)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	outCfg, err := config.LoadOutbound(os.Getenv, cfg)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	var sealer *replay.Sealer
	if replayCfg.OverrideKey.Reveal() != "" {
		if sealer, err = replay.NewSealer([]byte(replayCfg.OverrideKey.Reveal())); err != nil {
			logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
				Error("invalid replay override key", slog.String("error", err.Error()))
			return 1
		}
	}
	tokenKeys, err := token.NewKeys(endpointCfg.HMACKey(), endpointCfg.EncKey(), 1)
	if err != nil {
		logging.New(os.Stderr, "api", string(cfg.Env), slog.LevelInfo).
			Error("invalid endpoint token keys", slog.String("error", err.Error()))
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

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Ingestion removes sensitive values and the explorer marks them; both use
	// the same rules so markers match what was removed.
	redactor := redact.New(ingestCfg.RedactExtraHeaders, ingestCfg.RedactExtraParams)
	ingestHandler := ingestion.NewHandler(ingestion.Options{
		Store:             ingestion.NewPGStore(pool),
		Hasher:            tokenKeys,
		Redactor:          redactor,
		Metrics:           ingestion.NewMetrics(registry),
		MaxBodyBytes:      cfg.Limits.IngestMaxBodyBytes,
		MaxHeaderFields:   cfg.Limits.IngestMaxHeaderFields,
		BodyTimeout:       time.Duration(cfg.Limits.IngestBodyTimeoutSeconds) * time.Second,
		EndpointPerMinute: cfg.Limits.IngestRatePerEndpointPerMinute,
		EndpointBurst:     cfg.Limits.IngestRateEndpointBurst,
		IPPerMinute:       cfg.Limits.IngestRatePerIPPerMinute,
		IPBurst:           cfg.Limits.IngestRateIPBurst,
		IngestHost:        separateIngestHost(endpointCfg.IngestBaseURL.Hostname(), cfg.AppOrigin.Hostname()),
	})

	// Live event stream: one LISTEN connection per instance fans out to
	// streams. Streams end when shutdown starts so it is not held up.
	listener, err := realtime.NewPGSource(cfg.DatabaseURL, logger)
	if err != nil {
		logger.Error("event listener setup failed", slog.String("error", err.Error()))
		return 1
	}
	streamMetrics := realtime.NewMetrics(registry)
	broker := realtime.NewBroker(realtime.Options{Source: listener, Logger: logger, OnSlowDrop: streamMetrics.SlowDrop})
	go broker.Run(ctx)
	validator := identity.NewValidator(authCfg, store.New(pool))
	replayGate := replay.NewGate(replayCfg.Enabled, store.New(pool))
	replayHandler := replay.NewHandler(
		replay.NewService(pool, redactor, replayGate, outbound.NewValidator(outbound.OptionsFrom(outCfg, "", nil, nil)),
			sealer, replayCfg, replay.NewRateLimiter(replayCfg.UserRatePerMinute)),
		replay.NewReads(pool, redactor), replayCfg.HistoryPageDefault, cfg.Limits.PageMax)
	streamsDone := make(chan struct{})

	handler := newRouter(routerDeps{
		cfg:       cfg,
		auth:      authCfg,
		logger:    logger,
		health:    checker,
		validator: validator,
		authProxy: identity.NewAuthProxy(identity.ProxyOptions{
			Target:      authCfg.ServiceURL,
			Secret:      authCfg.ProxySharedSecret,
			Timeout:     time.Duration(cfg.Limits.AuthProxyTimeoutSeconds) * time.Second,
			ForwardedTo: cfg.AppOrigin.Scheme,
		}),
		projects: projects.NewHandler(projects.NewService(pool, cfg.Limits.ProjectsPerUser)),
		endpoints: endpoints.NewHandler(endpoints.NewService(pool, tokenKeys,
			endpointCfg.IngestBaseURL.String(), cfg.Limits.EndpointsPerProject)),
		events: events.NewHandler(events.NewService(pool, redactor), cfg.Limits.PageDefault, cfg.Limits.PageMax).
			WithStream(events.StreamOptions{
				Broker:          broker,
				Slots:           realtime.NewSlots(cfg.Limits.StreamsPerUser),
				Metrics:         streamMetrics,
				Validator:       validator,
				RevalidateEvery: cfg.StreamRevalidate,
				Shutdown:        streamsDone,
			}),
		ingestion: ingestHandler,
		replay:    replayHandler,
		replayCfg: replayGate,
		retention: retentionCfg,
		web:       webHandler(),
	})
	srv := httpx.NewServer(cfg.HTTPAddr, handler, cfg.Limits.HTTPMaxHeaderBytes, logger)
	srv.RegisterOnShutdown(func() { close(streamsDone) })

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		logger.Error("listen failed", slog.String("addr", cfg.HTTPAddr), slog.String("error", err.Error()))
		return 1
	}
	logger.Info("api listening", slog.String("addr", ln.Addr().String()))

	if ingestCfg.MetricsAddr != "" {
		mln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ingestCfg.MetricsAddr)
		if err != nil {
			logger.Error("metrics listen failed", slog.String("addr", ingestCfg.MetricsAddr), slog.String("error", err.Error()))
			return 1
		}
		msrv := httpx.NewServer(ingestCfg.MetricsAddr, metricsMux(registry), cfg.Limits.HTTPMaxHeaderBytes, logger)
		go func() {
			if err := httpx.Serve(ctx, msrv, mln, cfg.ShutdownGrace, nil); err != nil {
				logger.Error("metrics server stopped with error", slog.String("error", err.Error()))
			}
		}()
		logger.Info("metrics listening", slog.String("addr", mln.Addr().String()))
	}
	if err := httpx.Serve(ctx, srv, ln, cfg.ShutdownGrace, checker.MarkShuttingDown); err != nil {
		logger.Error("server stopped with error", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("api stopped")
	return 0
}

// separateIngestHost returns the ingest host when it differs from the app
// host (a dedicated ingest domain); otherwise "" so ingestion answers on the
// shared app host.
func separateIngestHost(ingestHost, appHost string) string {
	if strings.EqualFold(ingestHost, appHost) {
		return ""
	}
	return ingestHost
}

// metricsMux serves /metrics for the private metrics listener only.
func metricsMux(reg *prometheus.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
}
