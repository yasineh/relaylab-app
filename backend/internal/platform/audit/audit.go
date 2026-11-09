// Package audit records who did what to which project resource (app.audit_log).
// Entries are written in the same transaction as the change they describe.
// Metadata must never contain tokens, URLs, secrets, or payloads.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"relaylab/internal/platform/db/store"
)

// Entry is one audited action.
type Entry struct {
	ProjectID    uuid.UUID
	ActorUserID  string // the session user; "" for system actions
	Action       string // e.g. "endpoint.created"
	ResourceType string // e.g. "endpoint"
	ResourceID   uuid.UUID
	Metadata     map[string]string
}

// Record inserts e using q (normally bound to the caller's transaction).
func Record(ctx context.Context, q *store.Queries, e Entry) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("audit id: %w", err)
	}
	meta := e.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("audit metadata: %w", err)
	}
	return q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		ProjectID:    pgtype.UUID{Bytes: e.ProjectID, Valid: true},
		ActorUserID:  pgtype.Text{String: e.ActorUserID, Valid: e.ActorUserID != ""},
		Action:       e.Action,
		ResourceType: e.ResourceType,
		ResourceID:   pgtype.UUID{Bytes: e.ResourceID, Valid: e.ResourceID != uuid.Nil},
		Metadata:     raw,
	})
}
