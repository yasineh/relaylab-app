// Package projects owns projects: the ownership and isolation boundary for
// every later resource. Every operation is scoped to the acting user, both
// here and in the SQL (owner_id in every WHERE clause).
package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/platform/db/store"
)

// Field limits (also enforced by CHECK constraints in the migration).
const (
	NameMaxChars        = 80
	DescriptionMaxChars = 500
)

var (
	// ErrNotFound covers missing projects, other users' projects, and malformed ids alike.
	ErrNotFound = errors.New("project not found")
	// ErrLimitReached means the owner already has the maximum number of projects.
	ErrLimitReached = errors.New("project limit reached")
)

// ValidationError maps request fields to user-facing problems.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return fmt.Sprintf("invalid project fields: %v", e.Fields) }

// Project is a project as returned to its owner.
type Project struct {
	ID          uuid.UUID
	Name        string
	Description *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Optional distinguishes "not provided" from "provided" (possibly null) in PATCH bodies.
type Optional[T any] struct {
	Set   bool
	Value T
}

// Update holds the fields of a partial update.
type Update struct {
	Name        Optional[string]
	Description Optional[*string]
}

// Service implements project operations for the acting user.
type Service struct {
	pool  *pgxpool.Pool
	limit int
}

// NewService returns a service enforcing limit projects per user.
func NewService(pool *pgxpool.Pool, limit int) *Service {
	return &Service{pool: pool, limit: limit}
}

// ParseID parses a project id from a URL. Anything that is not a UUID is
// reported as ErrNotFound, so malformed and foreign ids look identical.
func ParseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrNotFound
	}
	return id, nil
}

// Create adds a project for ownerID, enforcing the per-user limit even under
// concurrent requests (creates for one owner are serialised by an advisory lock).
func (s *Service) Create(ctx context.Context, ownerID, name string, description *string) (Project, error) {
	name, nameProblem := normalizeName(name)
	desc, descProblem := normalizeDescription(description)
	if err := problems(map[string]string{"name": nameProblem, "description": descProblem}); err != nil {
		return Project{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Project{}, fmt.Errorf("generate project id: %w", err)
	}

	var created store.AppProject
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockOwnerProjects(ctx, ownerID); err != nil {
			return err
		}
		n, err := q.CountProjects(ctx, ownerID)
		if err != nil {
			return err
		}
		if int(n) >= s.limit {
			return ErrLimitReached
		}
		created, err = q.CreateProject(ctx, store.CreateProjectParams{
			ID: pgUUID(id), OwnerID: ownerID, Name: name, Description: pgText(desc),
		})
		return err
	})
	if err != nil {
		if errors.Is(err, ErrLimitReached) {
			return Project{}, err
		}
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return fromRow(created.ID, created.Name, created.Description, created.CreatedAt, created.UpdatedAt), nil
}

// List returns the owner's projects, newest first.
func (s *Service) List(ctx context.Context, ownerID string) ([]Project, error) {
	rows, err := store.New(s.pool).ListProjects(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]Project, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r.ID, r.Name, r.Description, r.CreatedAt, r.UpdatedAt))
	}
	return out, nil
}

// Authorize returns the project if ownerID owns it, and ErrNotFound otherwise.
// Later modules call it before touching project-scoped data.
func (s *Service) Authorize(ctx context.Context, ownerID string, projectID uuid.UUID) (Project, error) {
	r, err := store.New(s.pool).GetProject(ctx, store.GetProjectParams{ID: pgUUID(projectID), OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return fromRow(r.ID, r.Name, r.Description, r.CreatedAt, r.UpdatedAt), nil
}

// Update changes the name and/or description of an owned project.
func (s *Service) Update(ctx context.Context, ownerID string, projectID uuid.UUID, u Update) (Project, error) {
	fields := map[string]string{}
	var name pgtype.Text
	if u.Name.Set {
		n, problem := normalizeName(u.Name.Value)
		fields["name"] = problem
		name = pgtype.Text{String: n, Valid: true}
	}
	var desc *string
	if u.Description.Set {
		var problem string
		desc, problem = normalizeDescription(u.Description.Value)
		fields["description"] = problem
	}
	if err := problems(fields); err != nil {
		return Project{}, err
	}
	if !u.Name.Set && !u.Description.Set {
		return Project{}, &ValidationError{Fields: map[string]string{"name": "Provide a name or description to update."}}
	}
	r, err := store.New(s.pool).UpdateProject(ctx, store.UpdateProjectParams{
		Name:           name,
		SetDescription: u.Description.Set,
		Description:    pgText(desc),
		ID:             pgUUID(projectID),
		OwnerID:        ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("update project: %w", err)
	}
	return fromRow(r.ID, r.Name, r.Description, r.CreatedAt, r.UpdatedAt), nil
}

// Delete permanently deletes an owned project. It is one statement: all
// project-scoped data goes through ON DELETE CASCADE (D18).
func (s *Service) Delete(ctx context.Context, ownerID string, projectID uuid.UUID) error {
	n, err := store.New(s.pool).DeleteProject(ctx, store.DeleteProjectParams{ID: pgUUID(projectID), OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// normalizeName trims the name and checks 1–80 characters with no control characters.
func normalizeName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(name); {
	case !utf8.ValidString(name):
		return name, "Name must be valid text."
	case n == 0:
		return name, "Enter a project name."
	case n > NameMaxChars:
		return name, fmt.Sprintf("Use at most %d characters.", NameMaxChars)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return name, "Name must not contain control characters."
	}
	return name, ""
}

// normalizeDescription trims the description; empty becomes nil. Line breaks
// and tabs are allowed, other control characters are not.
func normalizeDescription(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	d := strings.TrimSpace(*raw)
	if d == "" {
		return nil, ""
	}
	if !utf8.ValidString(d) {
		return nil, "Description must be valid text."
	}
	if utf8.RuneCountInString(d) > DescriptionMaxChars {
		return nil, fmt.Sprintf("Use at most %d characters.", DescriptionMaxChars)
	}
	if strings.IndexFunc(d, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return nil, "Description must not contain control characters."
	}
	return &d, ""
}

func problems(m map[string]string) error {
	out := map[string]string{}
	for k, v := range m {
		if v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &ValidationError{Fields: out}
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func pgText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func fromRow(id pgtype.UUID, name string, desc pgtype.Text, created, updated pgtype.Timestamptz) Project {
	p := Project{ID: id.Bytes, Name: name, CreatedAt: created.Time.UTC(), UpdatedAt: updated.Time.UTC()}
	if desc.Valid {
		d := desc.String
		p.Description = &d
	}
	return p
}
