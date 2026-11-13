package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/platform/db/store"
)

// ErrUnknownEndpoint means no endpoint has the presented token.
var ErrUnknownEndpoint = errors.New("unknown endpoint")

// endpointRef is what ingestion learns from a token: never the owner.
type endpointRef struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Enabled   bool
	Demo      bool // owned by a demo user: the demo rate limit applies
}

// event is one accepted, redacted request ready to store.
type event struct {
	ID         uuid.UUID
	Endpoint   endpointRef
	ReceivedAt time.Time
	Captured   captured
	Body       []byte
	Class      classification
	RequestID  string
	ReplayID   uuid.UUID // set for events stored by a replay to a test sink
}

// Store is the persistence ingestion needs. Tests substitute failures.
type Store interface {
	// Lookup finds an endpoint by token hash, or returns ErrUnknownEndpoint.
	Lookup(ctx context.Context, tokenHash []byte) (endpointRef, error)
	// Save inserts the event and updates the endpoint's last-received time in
	// one transaction. When it returns nil the event is durable.
	Save(ctx context.Context, ev event) error
	// Notify publishes a new-event notification for live updates. Called only
	// after Save commits; a failure never loses the event.
	Notify(ctx context.Context, projectID, eventID uuid.UUID) error
}

// PGStore implements Store on PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore returns a Store backed by pool (connected as app_runtime).
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

// Lookup implements Store.
func (s *PGStore) Lookup(ctx context.Context, tokenHash []byte) (endpointRef, error) {
	row, err := store.New(s.pool).GetEndpointByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return endpointRef{}, ErrUnknownEndpoint
	}
	if err != nil {
		return endpointRef{}, fmt.Errorf("lookup endpoint: %w", err)
	}
	return endpointRef{ID: row.ID.Bytes, ProjectID: row.ProjectID.Bytes, Enabled: row.Enabled, Demo: row.IsDemo}, nil
}

// insertParams turns a captured event into the insert row.
func insertParams(ev event) (store.InsertEventParams, error) {
	c := ev.Captured
	params, err := json.Marshal(c.Params)
	if err != nil {
		return store.InsertEventParams{}, fmt.Errorf("encode query params: %w", err)
	}
	headers, err := json.Marshal(c.Headers)
	if err != nil {
		return store.InsertEventParams{}, fmt.Errorf("encode headers: %w", err)
	}
	body := ev.Body
	if body == nil {
		body = []byte{} // nil would be sent as NULL
	}
	var ip *netip.Addr
	if c.SourceIPPrefix != nil && c.SourceIPPrefix.IsValid() {
		ip = c.SourceIPPrefix
	}
	arg := store.InsertEventParams{
		ID:                  pgUUID(ev.ID),
		EndpointID:          pgUUID(ev.Endpoint.ID),
		ProjectID:           pgUUID(ev.Endpoint.ProjectID),
		ReceivedAt:          pgtype.Timestamptz{Time: ev.ReceivedAt, Valid: true},
		Method:              c.Method,
		Path:                c.Path,
		RawQuery:            c.RawQuery,
		QueryParams:         params,
		Headers:             headers,
		RedactedHeaderCount: int16(min(c.RedactedHeaderCount, 32767)), //nolint:gosec // clamped
		ContentType:         optText(c.ContentType),
		Body:                body,
		BodySize:            int32(len(body)), //nolint:gosec // bounded by the ingest body limit (at most 1 MiB)
		BodyEncoding:        ev.Class.Encoding,
		BodyJson:            ev.Class.JSON,
		JsonInvalid:         ev.Class.JSONInvalid,
		SourceIpPrefix:      ip,
		UserAgent:           optText(c.UserAgent),
		CorrelationID:       ev.RequestID,
		Source:              "ingest",
	}
	if ev.ReplayID != uuid.Nil {
		arg.Source = "replay"
		arg.ReplayAttemptID = pgUUID(ev.ReplayID)
	}
	return arg, nil
}

// Save implements Store.
func (s *PGStore) Save(ctx context.Context, ev event) error {
	arg, err := insertParams(ev)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.InsertEvent(ctx, arg); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
		if err := q.TouchEndpointReceived(ctx, store.TouchEndpointReceivedParams{
			ReceivedAt: arg.ReceivedAt, ID: arg.EndpointID,
		}); err != nil {
			return fmt.Errorf("touch endpoint: %w", err)
		}
		return nil
	})
}

// Notify implements Store.
func (s *PGStore) Notify(ctx context.Context, projectID, eventID uuid.UUID) error {
	payload, err := json.Marshal(map[string]string{"project_id": projectID.String(), "event_id": eventID.String()})
	if err != nil {
		return err
	}
	return store.New(s.pool).NotifyEvent(ctx, string(payload))
}

func optText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
