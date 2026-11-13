package replay

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/platform/config"
	"relaylab/internal/replay/outbound"
	"relaylab/internal/testutil/fixture"
	"relaylab/internal/testutil/isolation"
)

// testKey is a test-only override encryption key.
var testKey = []byte("test-only-replay-override-key-32")

type apiEnv struct {
	*fixture.Env
	t          *testing.T
	h          chi.Router
	svc        *Service
	status     *fakeStatus
	limits     config.Replay
	alice, bob isolation.Requester
	project    uuid.UUID
	source     uuid.UUID // the endpoint events arrive on
	sink       uuid.UUID // another endpoint, the default test sink
	event      uuid.UUID
}

type apiOpts struct {
	disabled bool
	limits   func(*config.Replay)
	limiter  RateLimiter
}

func newAPIEnv(t *testing.T, o apiOpts) *apiEnv {
	t.Helper()
	e := &apiEnv{Env: fixture.New(t, "alice", "bob"), t: t, status: &fakeStatus{active: true}}
	e.limits = config.Replay{
		Enabled: !o.disabled, ProjectConcurrency: 2, ProjectQueueDepth: 20, UserRatePerMinute: 30, MaxTries: 3,
		LeaseSeconds: 60, MaxLeaseLosses: 3, ResponseExcerptBytes: 64 << 10, HistoryPageDefault: 25,
	}
	if o.limits != nil {
		o.limits(&e.limits)
	}
	sealer, err := NewSealer(testKey)
	if err != nil {
		t.Fatal(err)
	}
	validator := outbound.NewValidator(outbound.Options{AllowedPorts: []int{443, 8443}, BlockedHosts: []string{"relaylab.test"}})
	e.svc = NewService(e.Pool, red, NewGate(e.limits.Enabled, e.status), validator, sealer, &e.limits, o.limiter)
	h := NewHandler(e.svc, NewReads(e.Pool, red), e.limits.HistoryPageDefault, 100)
	e.h = e.Router(func(v1 chi.Router) {
		v1.Route("/projects/{projectId}", func(pr chi.Router) {
			pr.Route("/events/{eventId}/replays", h.EventRoutes)
			pr.Route("/replays", h.ProjectRoutes)
			pr.Route("/audit-log", h.AuditRoutes)
		})
	})
	e.alice, e.bob = e.As(t, e.h, "alice"), e.As(t, e.h, "bob")
	e.project = e.Project(t, "alice", "Alpha")
	e.source = e.endpoint(t, e.project, "Stripe", true)
	e.sink = e.endpoint(t, e.project, "Sink", true)
	e.event = e.storeEvent(t, e.project, e.source, [][2]string{
		{"Authorization", "[REDACTED]"}, {"Content-Type", "application/json"}, {"Cookie", "[REDACTED]"},
		{"Stripe-Signature", "[REDACTED]"}, {"X-Github-Event", "push"},
	}, []byte(`{"hello":"relaylab"}`), "application/json")
	return e
}

func (e *apiEnv) endpoint(t *testing.T, project uuid.UUID, name string, enabled bool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	hash := make([]byte, 32)
	_, _ = rand.Read(hash)
	if _, err := e.Admin.Exec(context.Background(),
		`INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext, enabled) VALUES ($1, $2, $3, $4, '\x00', $5)`,
		id, project, name, hash, enabled); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *apiEnv) storeEvent(t *testing.T, project, endpoint uuid.UUID, headers [][2]string, body []byte, ct string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	hj, _ := json.Marshal(headers)
	encoding := "utf8"
	if !json.Valid(body) && len(body) > 0 && body[0] == 0xff {
		encoding = "binary"
	}
	if _, err := e.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params, headers,
		  content_type, body, body_size, body_encoding, correlation_id)
		VALUES ($1, $2, $3, now(), 'POST', '/hooks', '{}', $4, $5, $6, $7, $8, 'req-1')`,
		id, endpoint, project, hj, ct, body, len(body), encoding); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *apiEnv) eventPath(event uuid.UUID) string {
	return "/api/v1/projects/" + e.project.String() + "/events/" + event.String() + "/replays"
}

func (e *apiEnv) count(table string) int {
	return e.Count(e.t, "SELECT count(*) FROM "+table)
}

func body(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func urlDest(u string) map[string]any {
	return map[string]any{"destination": map[string]any{"type": "url", "url": u}}
}

func sinkDest(id uuid.UUID) map[string]any {
	return map[string]any{"destination": map[string]any{"type": "sink", "endpoint_id": id.String()}}
}

type errEnvelope struct {
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %d %s: %v", rec.Code, rec.Body, err)
	}
	return v
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errEnvelope {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	env := decode[errEnvelope](t, rec)
	if env.Error.Code != code {
		t.Fatalf("code %q, want %q: %s", env.Error.Code, code, rec.Body)
	}
	return env
}
