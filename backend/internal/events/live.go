package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"relaylab/internal/platform/db/store"
)

// Stream reads (add-live-event-stream). Like every event read, they are
// scoped to the owner in SQL.

// CheckProject returns ErrNotFound unless owner owns project.
func (s *Service) CheckProject(ctx context.Context, owner string, project uuid.UUID) error {
	if _, err := s.q.GetProject(ctx, store.GetProjectParams{ID: pgUUID(project), OwnerID: owner}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check project: %w", err)
	}
	return nil
}

// Summaries returns the summaries of ids that are events in owner's project,
// oldest first. Other ids are left out.
func (s *Service) Summaries(ctx context.Context, owner string, project uuid.UUID, ids []uuid.UUID) ([]Summary, error) {
	pg := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pg[i] = pgUUID(id)
	}
	rows, err := s.q.GetEventSummaries(ctx, store.GetEventSummariesParams{Ids: pg, ProjectID: pgUUID(project), OwnerID: owner})
	if err != nil {
		return nil, fmt.Errorf("event summaries: %w", err)
	}
	return toSummaries(convertRows(rows)), nil
}

// ReceivedAt returns when an event in owner's project was received, or
// ErrNotFound for any other id.
func (s *Service) ReceivedAt(ctx context.Context, owner string, project, id uuid.UUID) (time.Time, error) {
	t, err := s.q.GetEventReceivedAt(ctx, store.GetEventReceivedAtParams{ID: pgUUID(id), ProjectID: pgUUID(project), OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("event received time: %w", err)
	}
	return t.Time.UTC(), nil
}

// ReceivedSince returns up to n events received at or after since, oldest first.
func (s *Service) ReceivedSince(ctx context.Context, owner string, project uuid.UUID, since time.Time, n int) ([]Summary, error) {
	rows, err := s.q.ListEventsReceivedSince(ctx, store.ListEventsReceivedSinceParams{
		ProjectID: pgUUID(project), OwnerID: owner,
		Since:    pgtype.Timestamptz{Time: since, Valid: true},
		RowLimit: int32(n), //nolint:gosec // n is the backfill cap plus one
	})
	if err != nil {
		return nil, fmt.Errorf("events since: %w", err)
	}
	return toSummaries(convertRows(rows)), nil
}
