// Package fixture builds a migrated test database with users and real Better
// Auth sessions, for module integration and handler tests.
package fixture

import (
	"context"
	"log/slog"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/identity"
	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/dbtest"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/testutil/isolation"
)

// Origin is APP_ORIGIN for handler tests.
const Origin = "http://relaylab.test"

// Env is a migrated database with the runtime pool and a superuser connection.
type Env struct {
	DB    *dbtest.DB
	Pool  *pgxpool.Pool // app_runtime
	Admin *pgx.Conn     // superuser, for assertions and seeding
}

// New migrates a fresh database (auth schema first) and seeds the given users.
func New(t *testing.T, users ...string) *Env {
	t.Helper()
	d := dbtest.New(t)
	authtest.Prepare(t, d)
	ctx := context.Background()
	if err := db.Migrate(ctx, config.NewSecret(d.MigratorURL), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, config.NewSecret(d.RuntimeURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	for _, u := range users {
		authtest.SeedUser(t, d, u)
	}
	return &Env{DB: d, Pool: pool, Admin: admin}
}

// Project inserts a project owned by owner and returns its id.
func (e *Env) Project(t *testing.T, owner, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := e.Admin.Exec(context.Background(),
		`INSERT INTO app.projects (id, owner_id, name) VALUES ($1, $2, $3)`, id, owner, name); err != nil {
		t.Fatal(err)
	}
	return id
}

// Count runs a count(*) query as the superuser.
func (e *Env) Count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.Admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// Router mounts routes under /api/v1 behind the production middleware: request
// ID, body limit, same-origin and JSON checks, and real session validation.
func (e *Env) Router(mount func(v1 chi.Router)) chi.Router {
	appOrigin, _ := url.Parse(Origin)
	v := e.Validator()
	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware(slog.New(slog.DiscardHandler)), httpx.BodyLimit(config.DefaultLimits().APIMaxBodyBytes))
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Use(httpx.SameOrigin(appOrigin), httpx.RequireJSON, identity.RequireSession(v, true))
		mount(v1)
	})
	return r
}

// Validator returns the session validator the test router uses.
func (e *Env) Validator() *identity.Validator {
	return identity.NewValidator(&config.Auth{
		CookieName:    config.SessionCookieDev,
		SessionSecret: config.NewSecret(authtest.SessionSecret),
	}, store.New(e.Pool))
}

// As returns a requester signed in as user (a fresh real session).
func (e *Env) As(t *testing.T, h chi.Router, user string) isolation.Requester {
	t.Helper()
	return isolation.Requester{Handler: h, Origin: Origin, Cookie: authtest.SessionCookie(t, e.DB, user)}
}
