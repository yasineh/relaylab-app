package ingestion

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/redact"
)

// SeedRequest is a synthetic webhook for a demo workspace
// (add-production-readiness). It is stored exactly as a received request
// would be: the same header and query redaction and body classification.
type SeedRequest struct {
	Method     string
	Path       string
	RawQuery   string
	Headers    [][2]string
	Body       []byte
	ReceivedAt time.Time
	RequestID  string
}

// SeedEvent inserts req as an event of endpointID in projectID through q,
// which belongs to the caller's transaction. No notification is sent: the
// workspace is new, so nobody is listening yet.
func SeedEvent(ctx context.Context, q *store.Queries, red *redact.Redactor, projectID, endpointID uuid.UUID, req SeedRequest) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	sorted := append([][2]string(nil), req.Headers...)
	for i := range sorted {
		sorted[i][0] = http.CanonicalHeaderKey(sorted[i][0])
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	headers, redacted := red.Headers(sorted)
	c := captured{Method: req.Method, Path: req.Path, Headers: headers, RedactedHeaderCount: redacted, Params: map[string][]string{}}
	rawQuery, params, _ := red.Query(req.RawQuery)
	c.RawQuery = rawQuery
	for _, p := range params {
		c.Params[p.Name] = append(c.Params[p.Name], p.Value)
	}
	ct := ""
	for _, h := range headers {
		switch {
		case strings.EqualFold(h[0], "Content-Type"):
			v := h[1]
			c.ContentType, ct = &v, v
		case strings.EqualFold(h[0], "User-Agent"):
			v := h[1]
			c.UserAgent = &v
		}
	}
	ev := event{
		ID:         id,
		Endpoint:   endpointRef{ID: endpointID, ProjectID: projectID, Enabled: true},
		ReceivedAt: req.ReceivedAt.UTC(),
		Captured:   c,
		Body:       req.Body,
		Class:      classify(ct, req.Body),
		RequestID:  req.RequestID,
	}
	arg, err := insertParams(ev)
	if err != nil {
		return err
	}
	if err := q.InsertEvent(ctx, arg); err != nil {
		return fmt.Errorf("insert seed event: %w", err)
	}
	return q.TouchEndpointReceived(ctx, store.TouchEndpointReceivedParams{
		ReceivedAt: pgtype.Timestamptz{Time: ev.ReceivedAt, Valid: true}, ID: arg.EndpointID,
	})
}
