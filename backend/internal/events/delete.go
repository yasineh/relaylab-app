package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/db/store"
)

const (
	// clearBatchRows caps the rows one DELETE removes when clearing an endpoint.
	clearBatchRows = 1000
	// clearBudget bounds one clear request; the client repeats it until
	// it reports nothing left (deleted: 0).
	clearBudget = 30 * time.Second
)

// Delete permanently deletes one event and, by cascade, its replay attempts.
// Events the owner cannot see are ErrNotFound.
func (s *Service) Delete(ctx context.Context, owner string, project, id uuid.UUID) error {
	n, err := s.q.DeleteEvent(ctx, store.DeleteEventParams{ID: pgUUID(id), ProjectID: pgUUID(project), OwnerID: owner})
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearEndpoint deletes the endpoint's events in batches, for at most
// clearBudget, and returns how many went. A result of 0 means none are left.
// An endpoint the owner cannot see is ErrNotFound.
func (s *Service) ClearEndpoint(ctx context.Context, owner string, project, endpoint uuid.UUID) (int64, error) {
	if _, err := s.q.GetOwnedEndpointID(ctx, store.GetOwnedEndpointIDParams{
		ID: pgUUID(endpoint), ProjectID: pgUUID(project), OwnerID: owner,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("check endpoint: %w", err)
	}
	deadline := time.Now().Add(s.clearBudget())
	var total int64
	for {
		n, err := s.q.DeleteEndpointEventsBatch(ctx, store.DeleteEndpointEventsBatchParams{
			EndpointID: pgUUID(endpoint), ProjectID: pgUUID(project), OwnerID: owner, RowLimit: clearBatchRows,
		})
		if err != nil {
			if total > 0 && ctx.Err() == nil {
				return total, nil // progress is committed; the next call continues
			}
			return total, fmt.Errorf("clear endpoint events: %w", err)
		}
		total += n
		if n < clearBatchRows || time.Now().After(deadline) {
			return total, nil
		}
	}
}

func (s *Service) clearBudget() time.Duration {
	if s.clearLimit > 0 {
		return s.clearLimit
	}
	return clearBudget
}
