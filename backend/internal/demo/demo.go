// Package demo seeds demo workspaces (add-production-readiness, D16). A demo
// user is a Better Auth anonymous user; on their first dashboard request the
// API gives them one project with a primary endpoint, a test-sink endpoint,
// and synthetic events from seed.json, all in one transaction.
package demo

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/endpoints/token"
	"relaylab/internal/identity"
	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/httpx"
	"relaylab/internal/platform/logging"
	"relaylab/internal/platform/redact"
)

//go:embed seed.json
var seedJSON []byte

// Seed is the parsed seed.json.
type Seed struct {
	Project struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"project"`
	Endpoint string      `json:"endpoint"`
	Sink     string      `json:"sink"`
	Events   []SeedEvent `json:"events"`
}

// SeedEvent is one synthetic webhook.
type SeedEvent struct {
	MinutesAgo int         `json:"minutes_ago"`
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	Query      string      `json:"query"`
	Headers    [][2]string `json:"headers"`
	Body       string      `json:"body"`
}

// LoadSeed parses the embedded fixture.
func LoadSeed() (Seed, error) {
	var s Seed
	if err := json.Unmarshal(seedJSON, &s); err != nil {
		return Seed{}, fmt.Errorf("parse demo seed: %w", err)
	}
	return s, nil
}

// Provisioner seeds demo workspaces.
type Provisioner struct {
	pool *pgxpool.Pool
	keys *token.Keys
	red  *redact.Redactor
	cfg  *config.Demo
	seed Seed
	now  func() time.Time
	done sync.Map // user id -> struct{}: seeded, skip the check
}

// NewProvisioner returns a provisioner. red must match ingestion's.
func NewProvisioner(pool *pgxpool.Pool, keys *token.Keys, red *redact.Redactor, cfg *config.Demo) (*Provisioner, error) {
	s, err := LoadSeed()
	if err != nil {
		return nil, err
	}
	return &Provisioner{pool: pool, keys: keys, red: red, cfg: cfg, seed: s, now: time.Now}, nil
}

// Ensure seeds the user's workspace once. Concurrent first requests wait on
// a per-user lock; only the first one seeds. It returns whether this call
// seeded.
func (p *Provisioner) Ensure(ctx context.Context, id identity.Identity) (bool, error) {
	if !id.IsDemo {
		return false, nil
	}
	if _, ok := p.done.Load(id.UserID); ok {
		return false, nil
	}
	seeded := false
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockDemoWorkspace(ctx, id.UserID); err != nil {
			return fmt.Errorf("lock demo workspace: %w", err)
		}
		n, err := q.InsertDemoWorkspace(ctx, store.InsertDemoWorkspaceParams{
			UserID: id.UserID, ExpiresAt: pgtype.Timestamptz{Time: id.DemoExpiresAt, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("insert demo workspace: %w", err)
		}
		if n == 0 {
			return nil // seeded by an earlier request
		}
		seeded = true
		return p.seedInto(ctx, q, id.UserID)
	})
	if err != nil {
		return false, err
	}
	p.done.Store(id.UserID, struct{}{})
	return seeded, nil
}

func (p *Provisioner) seedInto(ctx context.Context, q *store.Queries, owner string) error {
	projectID := uuid.Must(uuid.NewV7())
	desc := p.seed.Project.Description
	if _, err := q.CreateProject(ctx, store.CreateProjectParams{
		ID: pgUUID(projectID), OwnerID: owner, Name: p.seed.Project.Name, Description: pgtype.Text{String: desc, Valid: desc != ""},
	}); err != nil {
		return fmt.Errorf("create demo project: %w", err)
	}
	endpointID, err := p.endpoint(ctx, q, projectID, p.seed.Endpoint)
	if err != nil {
		return err
	}
	if _, err := p.endpoint(ctx, q, projectID, p.seed.Sink); err != nil {
		return err
	}
	now := p.now().UTC()
	for i, e := range p.seed.Events {
		if err := ingestion.SeedEvent(ctx, q, p.red, projectID, endpointID, ingestion.SeedRequest{
			Method: e.Method, Path: e.Path, RawQuery: e.Query, Headers: e.Headers, Body: []byte(e.Body),
			ReceivedAt: now.Add(-time.Duration(e.MinutesAgo) * time.Minute),
			RequestID:  fmt.Sprintf("demo-seed-%d", i+1),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provisioner) endpoint(ctx context.Context, q *store.Queries, projectID uuid.UUID, name string) (uuid.UUID, error) {
	id := uuid.Must(uuid.NewV7())
	tok, err := token.Generate()
	if err != nil {
		return uuid.Nil, err
	}
	sealed, err := p.keys.Seal(tok, token.AAD(projectID, id))
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := q.CreateEndpoint(ctx, store.CreateEndpointParams{
		ID: pgUUID(id), ProjectID: pgUUID(projectID), Name: name,
		TokenHash: p.keys.Hash(tok), TokenCiphertext: sealed, TokenKeyVersion: p.keys.Version(),
	}); err != nil {
		return uuid.Nil, fmt.Errorf("create demo endpoint: %w", err)
	}
	return id, nil
}

// Middleware seeds a demo user's workspace before their first dashboard
// request is handled. Mount it after identity.RequireSession.
func (p *Provisioner) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := identity.FromContext(r.Context()); ok && id.IsDemo {
			seeded, err := p.Ensure(r.Context(), id)
			if err != nil {
				logging.FromContext(r.Context()).Error("demo workspace setup failed", slog.String("error", err.Error()))
				httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
				return
			}
			if seeded {
				logging.FromContext(r.Context()).Info("demo workspace seeded")
			}
		}
		next.ServeHTTP(w, r)
	})
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
