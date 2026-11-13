package replay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/identity"
	"relaylab/internal/platform/audit"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/redact"
	"relaylab/internal/replay/outbound"
)

// JobKind is the jobs.kind of replay deliveries.
const JobKind = "replay.deliver"

// idempotencyWindow is how long an Idempotency-Key is honoured (spec).
const idempotencyWindow = 24 * time.Hour

// MaxIdempotencyKey is the longest Idempotency-Key accepted (spec).
const MaxIdempotencyKey = 128

// Errors the handlers map to responses.
var (
	ErrNotFound  = errors.New("not found")
	ErrQueueFull = errors.New("replay queue full")
	// ErrDemoRestricted: demo users replay to their test sink only (403).
	ErrDemoRestricted      = errors.New("demo users may replay to the test sink only")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
)

// FieldError is a 422: Code is validation_failed or invalid_destination.
type FieldError struct {
	Code   string
	Fields map[string]string
}

func (e *FieldError) Error() string { return e.Code }

// RateLimitedError is a 429 rate_limited with a retry delay.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "rate limited" }

// Input is a preview or create request.
type Input struct {
	Destination struct {
		Type       string `json:"type"`
		URL        string `json:"url,omitempty"`
		EndpointID string `json:"endpoint_id,omitempty"`
	} `json:"destination"`
	Method          *string           `json:"method,omitempty"`
	HeaderOverrides map[string]string `json:"header_overrides,omitempty"`
}

// RateLimiter limits replay creations per user (in process, D6).
type RateLimiter interface {
	Allow(user string) (bool, time.Duration)
}

// Service creates and previews replays for the acting user.
type Service struct {
	pool      *pgxpool.Pool
	red       *redact.Redactor
	gate      *Gate
	validator *outbound.Validator
	sealer    *Sealer
	limits    *config.Replay
	limiter   RateLimiter // nil: no per-user rate limit
	now       func() time.Time
}

// NewService returns the replay service. sealer may be nil only when replay
// is disabled.
func NewService(pool *pgxpool.Pool, red *redact.Redactor, gate *Gate, validator *outbound.Validator,
	sealer *Sealer, limits *config.Replay, limiter RateLimiter,
) *Service {
	return &Service{pool: pool, red: red, gate: gate, validator: validator, sealer: sealer, limits: limits, limiter: limiter, now: time.Now}
}

// Gate returns the availability gate.
func (s *Service) Gate() *Gate { return s.gate }

// resolved is a validated request.
type resolved struct {
	destType  string
	targetURL string
	sink      *store.GetSinkEndpointRow
	method    string
	overrides []Override
	event     StoredEvent
	eventID   uuid.UUID
}

// resolve loads the event and validates the input. The order is: event
// (404), input (422), sink endpoint (404), availability (503).
func (s *Service) resolve(ctx context.Context, owner string, project, eventID uuid.UUID, in Input) (resolved, error) {
	q := store.New(s.pool)
	row, err := q.GetEventForReplay(ctx, store.GetEventForReplayParams{ID: pgUUID(eventID), ProjectID: pgUUID(project), OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return resolved{}, ErrNotFound
	}
	if err != nil {
		return resolved{}, fmt.Errorf("load event: %w", err)
	}
	var headers [][2]string
	if err := json.Unmarshal(row.Headers, &headers); err != nil {
		return resolved{}, fmt.Errorf("decode headers: %w", err)
	}
	r := resolved{eventID: eventID, event: StoredEvent{Method: row.Method, Headers: headers, Body: row.Body}}

	fields := map[string]string{}
	r.destType = in.Destination.Type
	if id, ok := identity.FromContext(ctx); ok && id.IsDemo && r.destType == DestinationURL {
		return resolved{}, ErrDemoRestricted
	}
	var sinkID uuid.UUID
	switch r.destType {
	case DestinationURL:
		if in.Destination.URL == "" {
			fields["destination.url"] = "Enter the URL to send the replay to."
		}
	case DestinationSink:
		id, err := uuid.Parse(in.Destination.EndpointID)
		if err != nil || id == uuid.Nil {
			fields["destination.endpoint_id"] = "Choose an endpoint of this project."
		}
		sinkID = id
	default:
		fields["destination.type"] = "Must be url or sink."
	}
	r.method = row.Method
	if in.Method != nil {
		r.method = *in.Method
	}
	if !slices.Contains(Methods, r.method) {
		fields["method"] = "Must be GET, POST, PUT, PATCH, or DELETE."
	}
	for name, value := range in.HeaderOverrides {
		r.overrides = append(r.overrides, Override{Name: name, Value: value})
	}
	sort.Slice(r.overrides, func(i, j int) bool { return r.overrides[i].Name < r.overrides[j].Name })
	if msg := ValidateOverrides(r.overrides); msg != "" {
		fields["header_overrides"] = msg
	} else if len(r.overrides) > 0 && s.sealer == nil {
		// Values are stored only encrypted; without REPLAY_OVERRIDE_ENC_KEY they cannot be stored.
		fields["header_overrides"] = "Added headers are not available on this server."
	}
	if len(fields) > 0 {
		return resolved{}, &FieldError{Code: "validation_failed", Fields: fields}
	}

	if r.destType == DestinationURL {
		if ferr := s.validator.Validate(in.Destination.URL); ferr != nil {
			return resolved{}, &FieldError{Code: "invalid_destination", Fields: map[string]string{"destination.url": outbound.MessageFor(ferr.Category)}}
		}
		r.targetURL = in.Destination.URL
	} else {
		sink, err := q.GetSinkEndpoint(ctx, store.GetSinkEndpointParams{ID: pgUUID(sinkID), ProjectID: pgUUID(project), OwnerID: owner})
		if errors.Is(err, pgx.ErrNoRows) {
			return resolved{}, ErrNotFound
		}
		if err != nil {
			return resolved{}, fmt.Errorf("load sink endpoint: %w", err)
		}
		if !sink.Enabled {
			return resolved{}, &FieldError{Code: "invalid_destination", Fields: map[string]string{"destination.endpoint_id": "This endpoint is disabled. Enable it or choose another."}}
		}
		r.sink = &sink
	}
	if err := s.gate.Check(ctx, r.destType); err != nil {
		return resolved{}, err
	}
	return r, nil
}

// Preview is what a replay would send. Nothing is stored or sent.
type Preview struct {
	Method      string       `json:"method"`
	Target      string       `json:"target"`
	Destination Destination  `json:"destination"`
	Headers     []SentHeader `json:"headers"`
	Stripped    []string     `json:"stripped_headers"`
	BodySize    int          `json:"body_size"`
	ContentType *string      `json:"content_type"`
}

// Destination describes where a replay goes.
type Destination struct {
	Type         string  `json:"type"`
	URL          *string `json:"url,omitempty"`
	EndpointID   *string `json:"endpoint_id,omitempty"`
	EndpointName *string `json:"endpoint_name,omitempty"`
}

func destinationOf(r resolved) (Destination, string) {
	if r.destType == DestinationURL {
		u := r.targetURL
		return Destination{Type: DestinationURL, URL: &u}, u
	}
	id, name := uuid.UUID(r.sink.ID.Bytes).String(), r.sink.Name
	return Destination{Type: DestinationSink, EndpointID: &id, EndpointName: &name}, "Test sink: " + name
}

// previewReplayID stands in for the id a created replay will get.
var previewReplayID = uuid.MustParse("00000000-0000-7000-8000-000000000000")

// Preview validates the input and returns the request a replay would send.
func (s *Service) Preview(ctx context.Context, owner string, project, eventID uuid.UUID, in Input) (Preview, error) {
	r, err := s.resolve(ctx, owner, project, eventID, in)
	if err != nil {
		return Preview{}, err
	}
	b := BuildRequest(r.event, r.method, r.overrides, previewReplayID, s.red)
	for i := range b.Sent {
		if b.Sent[i].Name == ReplayIDHeader {
			b.Sent[i].Value = "(assigned when the replay is created)"
		}
	}
	dest, target := destinationOf(r)
	p := Preview{
		Method: r.method, Target: target, Destination: dest, Headers: b.Sent, Stripped: b.Stripped,
		BodySize: len(r.event.Body),
	}
	if p.Stripped == nil {
		p.Stripped = []string{}
	}
	if ct := b.Header.Get("Content-Type"); ct != "" {
		p.ContentType = &ct
	}
	return p, nil
}

// fingerprint identifies a create request for Idempotency-Key comparison.
func fingerprint(eventID uuid.UUID, r resolved) []byte {
	canonical, _ := json.Marshal(struct {
		Event     string     `json:"event"`
		Type      string     `json:"type"`
		URL       string     `json:"url"`
		Sink      string     `json:"sink"`
		Method    string     `json:"method"`
		Overrides []Override `json:"overrides"`
	}{eventID.String(), r.destType, r.targetURL, sinkString(r), r.method, r.overrides})
	sum := sha256.Sum256(canonical)
	return sum[:]
}

func sinkString(r resolved) string {
	if r.sink == nil {
		return ""
	}
	return uuid.UUID(r.sink.ID.Bytes).String()
}

// Create queues a replay. created is false when an Idempotency-Key matched an
// earlier identical request, whose replay id is returned.
func (s *Service) Create(ctx context.Context, owner string, project, eventID uuid.UUID, in Input, idemKey string) (uuid.UUID, bool, error) {
	r, err := s.resolve(ctx, owner, project, eventID, in)
	if err != nil {
		return uuid.Nil, false, err
	}
	fp := fingerprint(eventID, r)
	if idemKey != "" {
		if id, ok, err := s.findIdempotent(ctx, project, idemKey, fp); err != nil || ok {
			return id, false, err
		}
	}
	if s.limiter != nil {
		if ok, wait := s.limiter.Allow(owner); !ok {
			return uuid.Nil, false, &RateLimitedError{RetryAfter: wait}
		}
	}
	id, err := s.insert(ctx, owner, project, r, idemKey, fp)
	var pgErr *pgconn.PgError
	if idemKey != "" && errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// A concurrent request with the same key won the race.
		if id, ok, err := s.findIdempotent(ctx, project, idemKey, fp); err != nil || ok {
			return id, false, err
		}
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// findIdempotent returns the replay created earlier with key, if it is
// within the window and the request matches; a different request is a
// conflict. Keys older than the window are released for reuse.
func (s *Service) findIdempotent(ctx context.Context, project uuid.UUID, key string, fp []byte) (uuid.UUID, bool, error) {
	q := store.New(s.pool)
	prev, err := q.GetReplayByIdempotencyKey(ctx, store.GetReplayByIdempotencyKeyParams{ProjectID: pgUUID(project), IdempotencyKey: pgtype.Text{String: key, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("idempotency lookup: %w", err)
	}
	if s.now().Sub(prev.CreatedAt.Time) > idempotencyWindow {
		if err := q.ExpireIdempotencyKey(ctx, store.ExpireIdempotencyKeyParams{ProjectID: pgUUID(project), IdempotencyKey: pgtype.Text{String: key, Valid: true}}); err != nil {
			return uuid.Nil, false, fmt.Errorf("expire idempotency key: %w", err)
		}
		return uuid.Nil, false, nil
	}
	if !bytes.Equal(prev.RequestFingerprint, fp) {
		return uuid.Nil, false, ErrIdempotencyConflict
	}
	return prev.ID.Bytes, true, nil
}

// insert writes the replay, its job, and the audit entry in one transaction,
// then wakes the workers. Override values are encrypted before the
// transaction, so plaintext never reaches the database.
func (s *Service) insert(ctx context.Context, owner string, project uuid.UUID, r resolved, idemKey string, fp []byte) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	jobID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	names := make([]string, len(r.overrides))
	var sealed []byte
	for i, o := range r.overrides {
		names[i] = o.Name
	}
	if len(r.overrides) > 0 {
		if s.sealer == nil {
			return uuid.Nil, errors.New("override key not configured")
		}
		if sealed, err = s.sealer.Seal(r.overrides, id); err != nil {
			return uuid.Nil, err
		}
	}
	payload, _ := json.Marshal(map[string]string{"replay_id": id.String()})
	meta := map[string]string{"event_id": r.eventID.String(), "destination_type": r.destType, "method": r.method}
	arg := store.InsertReplayAttemptParams{
		ID: pgUUID(id), ProjectID: pgUUID(project), EventID: pgUUID(r.eventID),
		ActorUserID: pgtype.Text{String: owner, Valid: true}, DestinationType: r.destType, Method: r.method,
		HeaderOverrideNames: names, HeaderOverridesCiphertext: sealed, RequestFingerprint: fp,
	}
	if r.destType == DestinationURL {
		arg.TargetUrl = pgtype.Text{String: r.targetURL, Valid: true}
		meta["destination_host"] = hostOf(r.targetURL)
	} else {
		arg.SinkEndpointID = r.sink.ID
		meta["sink_endpoint_id"] = uuid.UUID(r.sink.ID.Bytes).String()
	}
	if idemKey != "" {
		arg.IdempotencyKey = pgtype.Text{String: idemKey, Valid: true}
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		// Lock the project's replay rows' count by serializing creations per
		// project, so the queue-depth check is exact.
		if err := q.LockProjectJobs(ctx, pgUUID(project)); err != nil {
			return fmt.Errorf("lock project: %w", err)
		}
		active, err := q.CountActiveReplays(ctx, pgUUID(project))
		if err != nil {
			return fmt.Errorf("count active replays: %w", err)
		}
		if int(active) >= s.limits.ProjectQueueDepth {
			return ErrQueueFull
		}
		if err := q.InsertReplayAttempt(ctx, arg); err != nil {
			return fmt.Errorf("insert replay: %w", err)
		}
		if err := q.InsertJob(ctx, store.InsertJobParams{
			ID: pgUUID(jobID), Kind: JobKind, ProjectID: pgUUID(project), Payload: payload,
			MaxAttempts: int32(s.limits.MaxLeaseLosses), //nolint:gosec // bounded by config (at most 20)
		}); err != nil {
			return fmt.Errorf("insert job: %w", err)
		}
		if err := audit.Record(ctx, q, audit.Entry{
			ProjectID: project, ActorUserID: owner, Action: "replay.created", ResourceType: "replay",
			ResourceID: id, Metadata: meta,
		}); err != nil {
			return fmt.Errorf("audit: %w", err)
		}
		return q.NotifyJobs(ctx)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// hostOf returns a URL's host name only: no userinfo, port, path, or query
// (audit metadata and logs must not contain them).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
