package ingestion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/redact"
)

// ErrSinkDisabled means the target endpoint is disabled.
var ErrSinkDisabled = errors.New("endpoint disabled")

// InternalRequest is a request delivered to an endpoint without HTTP: a
// replay to the test sink (D12). No socket is opened, so nothing outside the
// database is reachable this way.
type InternalRequest struct {
	Method   string
	Headers  [][2]string // sent headers; sensitive values are redacted before storing
	Body     []byte
	ReplayID uuid.UUID
}

// StoreInternal stores req as a new event on endpointID, through the same
// insert and notification as public ingestion, minus HTTP, rate limits, and
// the token check. The event is marked source "replay" with the replay id.
// It returns ErrUnknownEndpoint or ErrSinkDisabled when the endpoint cannot
// receive.
func StoreInternal(ctx context.Context, s Store, q *store.Queries, red *redact.Redactor, endpointID uuid.UUID, req InternalRequest) (uuid.UUID, error) {
	ep, err := q.GetSinkForDelivery(ctx, pgUUID(endpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUnknownEndpoint
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load sink endpoint: %w", err)
	}
	if !ep.Enabled {
		return uuid.Nil, ErrSinkDisabled
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	sorted := append([][2]string(nil), req.Headers...)
	for i := range sorted {
		sorted[i][0] = http.CanonicalHeaderKey(sorted[i][0])
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	headers, redacted := red.Headers(sorted)
	var contentType *string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Content-Type") {
			ct := truncateChars(jsonSafe(h[1]), maxContentTypeChars)
			contentType = &ct
			break
		}
	}
	ua := "RelayLab-Replay"
	ct := ""
	if contentType != nil {
		ct = *contentType
	}
	ev := event{
		ID:         id,
		Endpoint:   endpointRef{ID: endpointID, ProjectID: ep.ProjectID.Bytes, Enabled: true},
		ReceivedAt: time.Now().UTC(),
		Captured: captured{
			Method: req.Method, Params: map[string][]string{}, Headers: headers, RedactedHeaderCount: redacted,
			ContentType: contentType, UserAgent: &ua,
		},
		Body:      req.Body,
		Class:     classify(ct, req.Body),
		RequestID: "replay-" + req.ReplayID.String(),
		ReplayID:  req.ReplayID,
	}
	if err := s.Save(ctx, ev); err != nil {
		return uuid.Nil, err
	}
	// Live streams see it like any other event; a failed notification only
	// delays them (the explorer recovers from the database).
	_ = s.Notify(ctx, ev.Endpoint.ProjectID, id)
	return id, nil
}
