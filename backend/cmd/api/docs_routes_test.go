package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"relaylab/internal/demo"
	"relaylab/internal/endpoints"
	"relaylab/internal/events"
	"relaylab/internal/identity"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/health"
	"relaylab/internal/platform/redact"
	"relaylab/internal/projects"
	"relaylab/internal/realtime"
	"relaylab/internal/replay"
)

// fullRouter is the production router with every optional route group
// mounted (replay, live stream, ingestion), for walking its routes.
func fullRouter(t *testing.T) chi.Routes {
	t.Helper()
	cfg, _ := config.Load(func(k string) string { return map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db"}[k] })
	authCfg, _ := config.LoadAuth(func(k string) string {
		return map[string]string{"BETTER_AUTH_SECRET": testSessionSecret, "AUTH_PROXY_SHARED_SECRET": testProxySecret}[k]
	}, cfg.Env)
	red := redact.New(nil, nil)
	ok := func(context.Context) error { return nil }
	h := newRouter(routerDeps{
		cfg: cfg, auth: authCfg, logger: slog.New(slog.DiscardHandler),
		health: health.New(ok, ok), validator: identity.NewValidator(authCfg, sessionStore{}), authProxy: http.NotFoundHandler(),
		projects:  projects.NewHandler(projects.NewService(nil, 20)),
		endpoints: endpoints.NewHandler(endpoints.NewService(nil, nil, "", 25)),
		events: events.NewHandler(events.NewService(nil, red), 50, 100).
			WithStream(events.StreamOptions{Broker: realtime.NewBroker(realtime.Options{}), Slots: realtime.NewSlots(1)}),
		replay:    replay.NewHandler(nil, nil, 25, 100),
		ingestion: ingestion.NewHandler(ingestion.Options{}),
		demo:      &demo.Provisioner{},
	})
	routes, isRoutes := h.(chi.Routes)
	if !isRoutes {
		t.Fatal("router does not expose its routes")
	}
	return routes
}

var docRouteRe = regexp.MustCompile("`(GET|POST|PUT|PATCH|DELETE) (/api/v1/[^`\\s]*)`")

// Every /api/v1 route the router registers is documented in docs/api.md as
// "`METHOD /path`", and docs/api.md lists no route that does not exist
// (spec: "Route coverage"). The ingestion, auth proxy, and health routes are
// checked by their sections.
func TestAPIDocsCoverEveryRoute(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	registered := map[string]bool{}
	other := map[string]bool{}
	err = chi.Walk(fullRouter(t), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.ReplaceAll(route, "/*/", "/")
		if route != "/" && route != "/api/v1/" {
			route = strings.TrimSuffix(route, "/")
		}
		switch {
		case strings.HasPrefix(route, "/api/v1/") || route == "/api/v1":
			if method != http.MethodHead {
				registered[method+" "+route] = true
			}
		default:
			other[route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) < 25 {
		t.Fatalf("only %d routes found: %v", len(registered), registered)
	}

	documented := map[string]bool{}
	for _, m := range docRouteRe.FindAllStringSubmatch(doc, -1) {
		documented[m[1]+" "+strings.SplitN(m[2], "?", 2)[0]] = true
	}
	var missing, extra []string
	for r := range registered {
		if !documented[r] {
			missing = append(missing, r)
		}
	}
	for r := range documented {
		if !registered[r] {
			extra = append(extra, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes missing from docs/api.md:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("docs/api.md lists routes that do not exist:\n  %s", strings.Join(extra, "\n  "))
	}

	for route, section := range map[string]string{
		"/h/{token}":  "## Webhook ingestion (`/h/*`)",
		"/api/auth/*": "## Authentication (`/api/auth/*`)",
		"/healthz":    "## Health",
		"/readyz":     "## Health",
	} {
		if !other[route] {
			t.Errorf("expected the router to serve %s", route)
		}
		if !strings.Contains(doc, section) || !strings.Contains(doc, route) {
			t.Errorf("%s is not documented under %q", route, section)
		}
	}
}
