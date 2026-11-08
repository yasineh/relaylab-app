package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/identity"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/projects"
)

type routerDeps struct {
	cfg       *config.Config
	auth      *config.Auth
	logger    *slog.Logger
	health    *health.Checker
	validator *identity.Validator
	authProxy http.Handler // /api/auth/* to the Better Auth service
	projects  *projects.Handler
	web       http.Handler // embedded SPA; nil when not built
}

// publicV1 lists the only /api/v1 routes reachable without a session.
var publicV1 = map[string]bool{
	http.MethodGet + " /api/v1/config":  true,
	http.MethodHead + " /api/v1/config": true,
}

// newRouter wires the middleware stack and route groups:
//
//	/api/auth/*  reverse proxy to the auth service (Better Auth checks Origin itself)
//	/api/v1/*    dashboard API: same-origin + JSON checks on unsafe methods, and a
//	             valid session on every route except GET /api/v1/config
//
// Later changes mount public ingestion under /h, which never reads the session cookie.
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
		api.Handle("/auth/*", d.authProxy)
		api.Route("/v1", func(v1 chi.Router) {
			v1.Use(
				httpx.SameOrigin(d.cfg.AppOrigin),
				httpx.RequireJSON,
				unlessPublic(identity.RequireSession(d.validator, d.auth.RequireEmailVerification)),
			)
			v1.NotFound(httpx.NotFound)
			v1.MethodNotAllowed(httpx.MethodNotAllowed)
			v1.Get("/config", publicConfig(d.auth))
			v1.Get("/me", identity.Me)
			v1.Route("/projects", d.projects.Routes)
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

// unlessPublic applies mw to every request except the publicV1 allowlist.
// Unknown /api/v1 paths also require a session, so route existence is not
// observable without signing in.
func unlessPublic(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		protected := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicV1[r.Method+" "+r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	}
}

// publicConfigResponse is the body of GET /api/v1/config. It contains no user
// data; later changes add retention and replay settings.
type publicConfigResponse struct {
	SignupEnabled             bool `json:"signup_enabled"`
	EmailVerificationRequired bool `json:"email_verification_required"`
}

func publicConfig(a *config.Auth) http.HandlerFunc {
	body := publicConfigResponse{
		SignupEnabled:             a.SignupEnabled,
		EmailVerificationRequired: a.RequireEmailVerification,
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		httpx.WriteJSON(w, http.StatusOK, body)
	}
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
