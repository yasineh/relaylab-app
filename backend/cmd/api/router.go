package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/endpoints"
	"relaylab/internal/events"
	"relaylab/internal/identity"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/projects"
	"relaylab/internal/replay"
)

type routerDeps struct {
	cfg       *config.Config
	auth      *config.Auth
	logger    *slog.Logger
	health    *health.Checker
	validator *identity.Validator
	authProxy http.Handler // /api/auth/* to the Better Auth service
	projects  *projects.Handler
	endpoints *endpoints.Handler
	events    *events.Handler
	replay    *replay.Handler    // nil in tests that do not need it
	replayCfg replayAvailability // nil: replay reported unavailable
	ingestion *ingestion.Handler // public /h/{token}; never reads the session
	web       http.Handler       // embedded SPA; nil when not built
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
//	/h/{token}*  public webhook ingestion: no session, CSRF, JSON, or CORS checks;
//	             it never reads the session cookie (it is stored redacted)
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
	if d.ingestion != nil {
		d.ingestion.Routes(r)
	}

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
			v1.Get("/config", publicConfig(d.auth, d.replayCfg))
			v1.Get("/me", identity.Me)
			v1.Route("/projects", func(pr chi.Router) {
				d.projects.Routes(pr)
				pr.Route("/{projectId}/endpoints", d.endpoints.Routes)
				pr.Route("/{projectId}/events", func(er chi.Router) {
					d.events.Routes(er)
					if d.replay != nil {
						er.Route("/{eventId}/replays", d.replay.EventRoutes)
					}
				})
				if d.replay != nil {
					pr.Route("/{projectId}/replays", d.replay.ProjectRoutes)
					pr.Route("/{projectId}/audit-log", d.replay.AuditRoutes)
				}
			})
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
// data; later changes add retention settings.
type publicConfigResponse struct {
	SignupEnabled             bool `json:"signup_enabled"`
	EmailVerificationRequired bool `json:"email_verification_required"`
	// ReplayEnabled is REPLAY_ENABLED; ReplayURLAvailable also requires a
	// recent worker heartbeat with an active outbound guard. Sink replays
	// need only ReplayEnabled.
	ReplayEnabled      bool `json:"replay_enabled"`
	ReplayURLAvailable bool `json:"replay_url_available"`
}

// replayAvailability is the replay gate as the config route needs it.
type replayAvailability interface {
	Enabled() bool
	URLAvailable(ctx context.Context) (bool, error)
}

func publicConfig(a *config.Auth, replayGate replayAvailability) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := publicConfigResponse{
			SignupEnabled:             a.SignupEnabled,
			EmailVerificationRequired: a.RequireEmailVerification,
		}
		if replayGate != nil {
			body.ReplayEnabled = replayGate.Enabled()
			// A heartbeat read error reports URL replay unavailable (fail closed).
			body.ReplayURLAvailable, _ = replayGate.URLAvailable(r.Context())
		}
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
	// /metrics is served only on the private METRICS_ADDR listener; here it is a 404.
	return p != "/healthz" && p != "/readyz" && p != "/metrics"
}
