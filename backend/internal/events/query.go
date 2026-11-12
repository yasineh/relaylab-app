// Package events is the read side of received webhooks: listing, filtering,
// paginating, and showing events to the owner of their project. Every query is
// scoped to the acting user in SQL (owner_id joined through app.projects).
package events

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"relaylab/internal/platform/db/store"
)

// Filter narrows a project's event list. Zero values mean "no filter".
type Filter struct {
	EndpointID        *uuid.UUID
	Methods           []string // upper-case HTTP methods
	ReceivedAfter     *time.Time
	ReceivedBefore    *time.Time
	ContentTypePrefix string
	Asc               bool // oldest first; default is newest first
}

// Summary is one row of the event list: no body, headers, or query.
type Summary struct {
	ID           uuid.UUID
	EndpointID   uuid.UUID
	EndpointName string
	ReceivedAt   time.Time
	Method       string
	Path         string
	ContentType  *string
	BodySize     int
}

// maxUUID is the upper id bound when nothing narrows it.
var maxUUID = uuid.UUID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

// idWindow folds the cursor and time filters into the exclusive id bounds the
// queries require. Event ids are UUIDv7 generated after received_at is taken
// (ingestion reads the body in between), so an id's timestamp is never earlier
// than its received_at but may be later. That makes received_after a safe
// lower id bound; received_before is not turned into an id bound and is only
// applied as the exact received_at condition.
func idWindow(f Filter, cursor *uuid.UUID) (after, before uuid.UUID) {
	after, before = uuid.Nil, maxUUID
	if f.ReceivedAfter != nil {
		after = lastIDOfMilli(f.ReceivedAfter.UnixMilli() - 1)
	}
	if cursor != nil {
		if f.Asc {
			if bytesGreater(*cursor, after) {
				after = *cursor
			}
		} else {
			before = *cursor
		}
	}
	return after, before
}

// lastIDOfMilli is the greatest UUIDv7 with the given millisecond timestamp,
// so every id from a later millisecond sorts after it.
func lastIDOfMilli(ms int64) uuid.UUID {
	if ms < 0 {
		return uuid.Nil
	}
	var u uuid.UUID
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(ms)) //nolint:gosec // ms >= 0
	copy(u[:6], ts[2:])
	for i := 6; i < 16; i++ {
		u[i] = 0xff
	}
	return u
}

func bytesGreater(a, b uuid.UUID) bool { return bytes.Compare(a[:], b[:]) > 0 }

// likePrefix escapes LIKE wildcards so the prefix matches literally.
func likePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p) + "%"
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func pgTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// listRows runs the list query matching the filter's endpoint and direction.
// It returns at most n rows from the exclusive id window (after, before).
func listRows(ctx context.Context, q store.Querier, owner string, project uuid.UUID, f Filter, after, before uuid.UUID, n int32) ([]Summary, error) {
	methods := f.Methods
	if methods == nil {
		methods = []string{}
	}
	var ct pgtype.Text
	if f.ContentTypePrefix != "" {
		ct = pgtype.Text{String: likePrefix(f.ContentTypePrefix), Valid: true}
	}
	// The four generated row types are identical; normalise them via one struct.
	common := store.ListEventsDescParams{
		ProjectID: pgUUID(project), OwnerID: owner, AfterID: pgUUID(after), BeforeID: pgUUID(before),
		Methods: methods, ReceivedAfter: pgTime(f.ReceivedAfter), ReceivedBefore: pgTime(f.ReceivedBefore),
		ContentTypePattern: ct, RowLimit: n,
	}
	var rows []store.ListEventsDescRow
	var err error
	switch {
	case f.EndpointID == nil && !f.Asc:
		rows, err = q.ListEventsDesc(ctx, common)
	case f.EndpointID == nil:
		var r []store.ListEventsAscRow
		r, err = q.ListEventsAsc(ctx, store.ListEventsAscParams(common))
		rows = convertRows(r)
	case !f.Asc:
		var r []store.ListEndpointEventsDescRow
		r, err = q.ListEndpointEventsDesc(ctx, endpointParams(common, *f.EndpointID))
		rows = convertRows(r)
	default:
		var r []store.ListEndpointEventsAscRow
		r, err = q.ListEndpointEventsAsc(ctx, store.ListEndpointEventsAscParams(endpointParams(common, *f.EndpointID)))
		rows = convertRows(r)
	}
	if err != nil {
		return nil, err
	}
	return toSummaries(rows), nil
}

func toSummaries(rows []store.ListEventsDescRow) []Summary {
	out := make([]Summary, len(rows))
	for i, r := range rows {
		out[i] = Summary{
			ID: r.ID.Bytes, EndpointID: r.EndpointID.Bytes, EndpointName: r.EndpointName,
			ReceivedAt: r.ReceivedAt.Time.UTC(), Method: r.Method, Path: r.Path,
			BodySize: int(r.BodySize),
		}
		if r.ContentType.Valid {
			ct := r.ContentType.String
			out[i].ContentType = &ct
		}
	}
	return out
}

type rowLike interface {
	store.ListEventsAscRow | store.ListEndpointEventsDescRow | store.ListEndpointEventsAscRow |
		store.GetEventSummariesRow | store.ListEventsReceivedSinceRow
}

func convertRows[R rowLike](in []R) []store.ListEventsDescRow {
	out := make([]store.ListEventsDescRow, len(in))
	for i, r := range in {
		out[i] = store.ListEventsDescRow(r)
	}
	return out
}

func endpointParams(c store.ListEventsDescParams, endpoint uuid.UUID) store.ListEndpointEventsDescParams {
	return store.ListEndpointEventsDescParams{
		EndpointID: pgUUID(endpoint), ProjectID: c.ProjectID, OwnerID: c.OwnerID,
		AfterID: c.AfterID, BeforeID: c.BeforeID, Methods: c.Methods,
		ReceivedAfter: c.ReceivedAfter, ReceivedBefore: c.ReceivedBefore,
		ContentTypePattern: c.ContentTypePattern, RowLimit: c.RowLimit,
	}
}

// listPage returns up to limit events after the cursor and, when more exist,
// the cursor for the next page. It fetches one extra row to know.
func listPage(ctx context.Context, q store.Querier, owner string, project uuid.UUID, f Filter, rawCursor string, limit int) ([]Summary, *string, error) {
	pos, err := decodeCursor(rawCursor, f)
	if err != nil {
		return nil, nil, err
	}
	after, before := idWindow(f, pos)
	rows, err := listRows(ctx, q, owner, project, f, after, before, int32(limit+1)) //nolint:gosec // limit <= LIMIT_PAGE_MAX
	if err != nil {
		return nil, nil, err
	}
	if len(rows) <= limit {
		return rows, nil, nil
	}
	rows = rows[:limit]
	next := encodeCursor(f, rows[limit-1].ID)
	return rows, &next, nil
}
