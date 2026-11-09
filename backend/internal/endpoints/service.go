// Package endpoints manages webhook receiving endpoints inside projects. Every
// dashboard operation is scoped to the session user through the project owner,
// in this service and in SQL.
package endpoints

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/endpoints/token"
	"relaylab/internal/platform/audit"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/logging"
)

// NameMaxChars is the display-name limit (also a CHECK constraint).
const NameMaxChars = 100

var (
	// ErrNotFound covers missing endpoints, foreign projects, endpoints of another
	// project, and malformed ids alike.
	ErrNotFound = errors.New("endpoint not found")
	// ErrLimitReached means the project already has the maximum number of endpoints.
	ErrLimitReached = errors.New("endpoint limit reached")
)

// ValidationError maps request fields to user-facing problems.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return fmt.Sprintf("invalid endpoint fields: %v", e.Fields) }

// Endpoint is an endpoint as shown to its owner.
type Endpoint struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	Name           string
	Enabled        bool
	URL            *string // nil only if the stored token cannot be decrypted (lost key); rotate to fix
	LastReceivedAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Service implements endpoint operations for the acting user.
type Service struct {
	pool       *pgxpool.Pool
	keys       *token.Keys
	ingestBase string
	limit      int
}

// NewService returns an endpoint service. ingestBase is INGEST_BASE_URL
// (an origin, without a trailing slash); limit is LIMIT_ENDPOINTS_PER_PROJECT.
func NewService(pool *pgxpool.Pool, keys *token.Keys, ingestBase string, limit int) *Service {
	return &Service{pool: pool, keys: keys, ingestBase: strings.TrimRight(ingestBase, "/"), limit: limit}
}

// ParseID parses an id from a URL; anything that is not a UUID is ErrNotFound.
func ParseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrNotFound
	}
	return id, nil
}

// Create adds an endpoint to an owned project, enforcing the per-project limit
// under a row lock on the project.
func (s *Service) Create(ctx context.Context, owner string, projectID uuid.UUID, name string) (Endpoint, error) {
	name, problem := normalizeName(name)
	if problem != "" {
		return Endpoint{}, &ValidationError{Fields: map[string]string{"name": problem}}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Endpoint{}, err
	}
	tok, err := token.Generate()
	if err != nil {
		return Endpoint{}, err
	}
	sealed, err := s.keys.Seal(tok, token.AAD(projectID, id))
	if err != nil {
		return Endpoint{}, err
	}

	var out Endpoint
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockOwnedProject(ctx, store.LockOwnedProjectParams{ID: pgUUID(projectID), OwnerID: owner}); err != nil {
			return notFound(err)
		}
		n, err := q.CountEndpoints(ctx, pgUUID(projectID))
		if err != nil {
			return err
		}
		if int(n) >= s.limit {
			return ErrLimitReached
		}
		r, err := q.CreateEndpoint(ctx, store.CreateEndpointParams{
			ID: pgUUID(id), ProjectID: pgUUID(projectID), Name: name,
			TokenHash: s.keys.Hash(tok), TokenCiphertext: sealed, TokenKeyVersion: s.keys.Version(),
		})
		if err != nil {
			return err
		}
		out = s.build(ctx, r.ID, r.ProjectID, r.Name, r.TokenCiphertext, r.Enabled, r.LastReceivedAt, r.CreatedAt, r.UpdatedAt)
		return s.audit(ctx, q, owner, out, "endpoint.created")
	})
	return out, wrap("create endpoint", err)
}

// List returns the endpoints of an owned project, newest first. A project the
// caller does not own is ErrNotFound (not an empty list).
func (s *Service) List(ctx context.Context, owner string, projectID uuid.UUID) ([]Endpoint, error) {
	q := store.New(s.pool)
	if _, err := q.GetProject(ctx, store.GetProjectParams{ID: pgUUID(projectID), OwnerID: owner}); err != nil {
		return nil, wrap("list endpoints", notFound(err))
	}
	rows, err := q.ListEndpoints(ctx, store.ListEndpointsParams{ProjectID: pgUUID(projectID), OwnerID: owner})
	if err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	out := make([]Endpoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.build(ctx, r.ID, r.ProjectID, r.Name, r.TokenCiphertext, r.Enabled, r.LastReceivedAt, r.CreatedAt, r.UpdatedAt))
	}
	return out, nil
}

// AuthorizeProject returns ErrNotFound unless owner owns projectID. Handlers
// call it before reading request bodies, so nothing about another user's
// project (not even body validation) is observable.
func (s *Service) AuthorizeProject(ctx context.Context, owner string, projectID uuid.UUID) error {
	_, err := store.New(s.pool).GetProject(ctx, store.GetProjectParams{ID: pgUUID(projectID), OwnerID: owner})
	return wrap("authorize project", notFound(err))
}

// Get returns one endpoint of an owned project.
func (s *Service) Get(ctx context.Context, owner string, projectID, id uuid.UUID) (Endpoint, error) {
	r, err := store.New(s.pool).GetEndpoint(ctx, store.GetEndpointParams{ID: pgUUID(id), ProjectID: pgUUID(projectID), OwnerID: owner})
	if err != nil {
		return Endpoint{}, wrap("get endpoint", notFound(err))
	}
	return s.build(ctx, r.ID, r.ProjectID, r.Name, r.TokenCiphertext, r.Enabled, r.LastReceivedAt, r.CreatedAt, r.UpdatedAt), nil
}

// Update renames and/or enables or disables an endpoint. The token and URL never change.
func (s *Service) Update(ctx context.Context, owner string, projectID, id uuid.UUID, name *string, enabled *bool) (Endpoint, error) {
	var newName pgtype.Text
	if name != nil {
		n, problem := normalizeName(*name)
		if problem != "" {
			return Endpoint{}, &ValidationError{Fields: map[string]string{"name": problem}}
		}
		newName = pgtype.Text{String: n, Valid: true}
	}
	if name == nil && enabled == nil {
		return Endpoint{}, &ValidationError{Fields: map[string]string{"name": "Provide a name or enabled state to update."}}
	}

	var out Endpoint
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		before, err := q.GetEndpoint(ctx, store.GetEndpointParams{ID: pgUUID(id), ProjectID: pgUUID(projectID), OwnerID: owner})
		if err != nil {
			return notFound(err)
		}
		var en pgtype.Bool
		if enabled != nil {
			en = pgtype.Bool{Bool: *enabled, Valid: true}
		}
		r, err := q.UpdateEndpoint(ctx, store.UpdateEndpointParams{
			Name: newName, Enabled: en, ID: pgUUID(id), ProjectID: pgUUID(projectID), OwnerID: owner,
		})
		if err != nil {
			return notFound(err)
		}
		out = s.build(ctx, r.ID, r.ProjectID, r.Name, r.TokenCiphertext, r.Enabled, r.LastReceivedAt, r.CreatedAt, r.UpdatedAt)
		if newName.Valid && newName.String != before.Name {
			if err := s.audit(ctx, q, owner, out, "endpoint.renamed"); err != nil {
				return err
			}
		}
		if enabled != nil && *enabled != before.Enabled {
			action := "endpoint.disabled"
			if *enabled {
				action = "endpoint.enabled"
			}
			return s.audit(ctx, q, owner, out, action)
		}
		return nil
	})
	return out, wrap("update endpoint", err)
}

// Rotate replaces the endpoint's token. The old URL stops working immediately;
// received events stay with the endpoint.
func (s *Service) Rotate(ctx context.Context, owner string, projectID, id uuid.UUID) (Endpoint, error) {
	tok, err := token.Generate()
	if err != nil {
		return Endpoint{}, err
	}
	sealed, err := s.keys.Seal(tok, token.AAD(projectID, id))
	if err != nil {
		return Endpoint{}, err
	}
	var out Endpoint
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := q.RotateEndpointToken(ctx, store.RotateEndpointTokenParams{
			TokenHash: s.keys.Hash(tok), TokenCiphertext: sealed, TokenKeyVersion: s.keys.Version(),
			ID: pgUUID(id), ProjectID: pgUUID(projectID), OwnerID: owner,
		})
		if err != nil {
			return notFound(err)
		}
		out = s.build(ctx, r.ID, r.ProjectID, r.Name, r.TokenCiphertext, r.Enabled, r.LastReceivedAt, r.CreatedAt, r.UpdatedAt)
		return s.audit(ctx, q, owner, out, "endpoint.rotated")
	})
	return out, wrap("rotate endpoint", err)
}

// Delete removes an endpoint in one statement; its events and replays go by cascade.
func (s *Service) Delete(ctx context.Context, owner string, projectID, id uuid.UUID) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := q.DeleteEndpoint(ctx, store.DeleteEndpointParams{ID: pgUUID(id), ProjectID: pgUUID(projectID), OwnerID: owner})
		if err != nil {
			return notFound(err)
		}
		return s.audit(ctx, q, owner, Endpoint{ID: r.ID.Bytes, ProjectID: projectID, Name: r.Name}, "endpoint.deleted")
	})
	return wrap("delete endpoint", err)
}

// URLFor returns the receiving URL for a token.
func (s *Service) URLFor(tok string) string { return s.ingestBase + "/h/" + tok }

func (s *Service) build(ctx context.Context, id, projectID pgtype.UUID, name string, sealed []byte, enabled bool,
	lastReceived, created, updated pgtype.Timestamptz) Endpoint {
	e := Endpoint{
		ID: id.Bytes, ProjectID: projectID.Bytes, Name: name, Enabled: enabled,
		CreatedAt: created.Time.UTC(), UpdatedAt: updated.Time.UTC(),
	}
	if lastReceived.Valid {
		t := lastReceived.Time.UTC()
		e.LastReceivedAt = &t
	}
	if tok, err := s.keys.Open(sealed, token.AAD(e.ProjectID, e.ID)); err == nil {
		u := s.URLFor(tok)
		e.URL = &u
	} else {
		// Never include the ciphertext or key details.
		logging.FromContext(ctx).Warn("endpoint token cannot be decrypted; rotate the endpoint to issue a new URL",
			slog.String("endpoint_id", e.ID.String()))
	}
	return e
}

// audit records an endpoint action. Metadata is limited to the id and name:
// never the token or URL.
func (s *Service) audit(ctx context.Context, q *store.Queries, owner string, e Endpoint, action string) error {
	return audit.Record(ctx, q, audit.Entry{
		ProjectID: e.ProjectID, ActorUserID: owner, Action: action,
		ResourceType: "endpoint", ResourceID: e.ID,
		Metadata: map[string]string{"endpoint_id": e.ID.String(), "name": e.Name},
	})
}

func normalizeName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(name); {
	case !utf8.ValidString(name):
		return name, "Name must be valid text."
	case n == 0:
		return name, "Enter an endpoint name."
	case n > NameMaxChars:
		return name, fmt.Sprintf("Use at most %d characters.", NameMaxChars)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return name, "Name must not contain control characters."
	}
	return name, ""
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func wrap(op string, err error) error {
	var ve *ValidationError
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrLimitReached) || errors.As(err, &ve) {
		return err
	}
	return fmt.Errorf("%s: %w", op, err)
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
