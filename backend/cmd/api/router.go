package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
)

type routerDeps struct {
	cfg    *config.Config
	logger *slog.Logger
	health *health.Checker
	web    http.Handler // embedded SPA; nil when not built
}

// newRouter wires the middleware stack and route groups. Later changes mount
// the dashboard API under /api/v1 and public ingestion under /h.
func newRouter(d routerDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(
		httpx.RequestIDMiddleware(d.logger),
		httpx.RealIP(d.cfg.TrustedProxies),
		httpx.AccessLog,
		httpx.Recoverer,
		httpx.SecurityHeaders,
		httpx.HeaderLimit(d.cfg.Limits.HTTPMaxHeaderBytes),
	)
	r.MethodNotAllowed(httpx.MethodNotAllowed)

	d.health.Register(r)

	r.Route("/api", func(api chi.Router) {
		api.Use(httpx.BodyLimit(d.cfg.Limits.APIMaxBodyBytes))
		api.NotFound(httpx.NotFound)
		api.MethodNotAllowed(httpx.MethodNotAllowed)
		api.Route("/v1", func(v1 chi.Router) {
			v1.NotFound(httpx.NotFound)
		})
	})

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if d.web != nil && isSPAPath(req) {
			d.web.ServeHTTP(w, req)
			return
		}
		httpx.NotFound(w, req)
	})
	return r
}

// isSPAPath reports whether a request should fall back to the web app.
func isSPAPath(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	for _, prefix := range []string{"/api", "/h"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return false
		}
	}
	return p != "/healthz" && p != "/readyz"
}
