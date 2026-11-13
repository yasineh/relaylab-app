package replay

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/platform/config"
	"relaylab/internal/replay/outbound"
	"relaylab/internal/testutil/fixture"
)

type scriptResolver struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
}

func (r *scriptResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.answers[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// execEnv delivers replays through the real guard to TLS test servers.
type execEnv struct {
	*fixture.Env
	t         *testing.T
	project   uuid.UUID
	source    uuid.UUID
	event     uuid.UUID
	limits    *config.Replay
	resolver  *scriptResolver
	reachable map[netip.AddrPort]bool
	opts      outbound.Options
	sealer    *Sealer
	runner    *Runner
	deliverer *Deliverer
	metrics   *Metrics
}

func newExecEnv(t *testing.T) *execEnv {
	t.Helper()
	e := &execEnv{Env: fixture.New(t, "alice"), t: t, limits: testLimits(), resolver: &scriptResolver{answers: map[string][]netip.Addr{}}, reachable: map[netip.AddrPort]bool{}}
	e.project = e.Project(t, "alice", "P")
	api := &apiEnv{Env: e.Env, t: t}
	e.source = api.endpoint(t, e.project, "Source", true)
	e.event = api.storeEvent(t, e.project, e.source, [][2]string{
		{"Authorization", "[REDACTED]"}, {"Content-Type", "application/json"}, {"Stripe-Signature", "[REDACTED]"},
		{"X-Github-Event", "push"},
	}, []byte(`{"hello":"relaylab"}`), "application/json")
	e.opts = outbound.Options{
		AllowedPorts: []int{443, 8443}, ConnectTimeout: time.Second, TLSHandshakeTimeout: time.Second,
		ResponseHeaderTimeout: 300 * time.Millisecond, TotalTimeout: 2 * time.Second, MaxResponseBytes: 64 << 10,
		MaxConcurrency: 8, UserAgent: "RelayLab-Replay/test",
	}
	var err error
	if e.sealer, err = NewSealer(testKey); err != nil {
		t.Fatal(err)
	}
	return e
}

// server starts a TLS destination reachable as name.
func (e *execEnv) server(name string, h http.HandlerFunc) string {
	e.t.Helper()
	s := httptest.NewTLSServer(h)
	e.t.Cleanup(s.Close)
	ap := netip.MustParseAddrPort(s.Listener.Addr().String())
	e.reachable[ap] = true
	e.opts.AllowedPorts = append(e.opts.AllowedPorts, int(ap.Port()))
	e.resolver.answers[name] = []netip.Addr{ap.Addr()}
	roots := s.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	e.opts = outbound.WithTestReachability(e.opts, func(a netip.AddrPort) bool { return e.reachable[a] }, e.resolver, roots)
	return "https://" + name + ":" + strconv.Itoa(int(ap.Port()))
}

// build creates the guard, runner, and deliverer after servers are set up.
func (e *execEnv) build() {
	if e.opts.TotalTimeout == 0 {
		e.t.Fatal("options not set")
	}
	if len(e.reachable) == 0 {
		e.opts = outbound.WithTestReachability(e.opts, func(netip.AddrPort) bool { return false }, e.resolver, nil)
	}
	client := outbound.New(e.opts)
	e.deliverer = NewDeliverer(DelivererOptions{
		Pool: e.Pool, Sender: client, Sealer: e.sealer, Red: red, Limits: e.limits, Metrics: e.metrics,
		Backoff: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
	})
	e.runner = NewRunner(RunnerOptions{Pool: e.Pool, WorkerID: "w1", Limits: e.limits, Slots: 4, Executor: e.deliverer, Metrics: e.metrics})
	e.deliverer.Bind(e.runner)
}

// queue creates a replay and its job directly (as the API would).
func (e *execEnv) queue(target string, sink *uuid.UUID, overrides []Override) uuid.UUID {
	e.t.Helper()
	id := uuid.Must(uuid.NewV7())
	names := []string{}
	var sealed []byte
	for _, o := range overrides {
		names = append(names, o.Name)
	}
	if len(overrides) > 0 {
		var err error
		if sealed, err = e.sealer.Seal(overrides, id); err != nil {
			e.t.Fatal(err)
		}
	}
	dest, targetURL := "url", any(target)
	if sink != nil {
		dest, targetURL = "sink", nil
	}
	if _, err := e.Admin.Exec(context.Background(), `INSERT INTO app.replay_attempts (id, project_id, event_id, actor_user_id, destination_type, target_url, sink_endpoint_id, method, header_override_names, header_overrides_ciphertext)
		VALUES ($1, $2, $3, 'alice', $4, $5, $6, 'POST', $7, $8)`, id, e.project, e.event, dest, targetURL, sink, names, sealed); err != nil {
		e.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"replay_id": id.String()})
	if _, err := e.Admin.Exec(context.Background(), `INSERT INTO app.jobs (id, kind, project_id, payload, max_attempts) VALUES ($1, 'replay.deliver', $2, $3, 3)`,
		uuid.Must(uuid.NewV7()), e.project, payload); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// runOne claims and executes the next job.
func (e *execEnv) runOne() {
	e.t.Helper()
	job, ok, err := e.runner.Claim(context.Background())
	if err != nil || !ok {
		e.t.Fatalf("claim: %v %v", ok, err)
	}
	e.deliverer.Execute(context.Background(), job)
}

func (e *execEnv) result(id uuid.UUID) ReplayJSON {
	e.t.Helper()
	d, err := NewReads(e.Pool, red).Get(context.Background(), "alice", e.project, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func TestDeliverySucceeds(t *testing.T) {
	e := newExecEnv(t)
	var got *http.Request
	var gotBody []byte
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Set-Cookie", "session=dest-secret")
		w.Header().Set("X-Api-Key", "dest-key")
		w.Header().Set("X-Request-Id", "abc")
		_, _ = w.Write([]byte("ok"))
	})
	e.build()
	id := e.queue(url+"/hook?token=abc", nil, []Override{{"Authorization", "Bearer s3cret"}})
	e.runOne()

	r := e.result(id)
	if r.State != "succeeded" || *r.Outcome != "succeeded" || r.Tries != 1 || *r.ResponseStatus != 200 ||
		r.Response == nil || *r.Response.Body.Text != "ok" || r.DurationMS == nil || r.FinishedAt == nil {
		t.Fatalf("result %+v", r)
	}
	if string(gotBody) != `{"hello":"relaylab"}` || got.URL.RequestURI() != "/hook?token=abc" ||
		got.Header.Get(ReplayIDHeader) != id.String() || got.Header.Get("Authorization") != "Bearer s3cret" ||
		got.Header.Get("Stripe-Signature") != "" || got.Header.Get("X-Github-Event") != "push" ||
		got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("destination saw %s %v %q", got.URL, got.Header, gotBody)
	}
	headers := map[string]SentHeader{}
	for _, h := range r.Response.Headers {
		headers[h.Name] = h
	}
	if headers["Set-Cookie"].Value != "[REDACTED]" || headers["X-Api-Key"].Value != "[REDACTED]" || headers["X-Request-Id"].Value != "abc" {
		t.Fatalf("response headers %v", r.Response.Headers)
	}
	if e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE header_overrides_ciphertext IS NOT NULL`) != 0 {
		t.Fatal("override ciphertext not erased")
	}
	if e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'done'`) != 1 {
		t.Fatal("job not done")
	}
}

func TestDestinationErrorIsSucceededOutcome(t *testing.T) {
	e := newExecEnv(t)
	var hits atomic.Int32
	url := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "oops", http.StatusInternalServerError)
	})
	e.build()
	id := e.queue(url, nil, nil)
	e.runOne()
	r := e.result(id)
	if r.State != "succeeded" || *r.ResponseStatus != 500 || !strings.HasPrefix(*r.Response.Body.Text, "oops") || r.Tries != 1 || hits.Load() != 1 {
		t.Fatalf("%+v (hits %d)", r, hits.Load())
	}
}

func TestRetriesOnGatewayErrorsUpToMaxTries(t *testing.T) {
	e := newExecEnv(t)
	var hits atomic.Int32
	url := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	e.build()
	id := e.queue(url, nil, nil)
	e.runOne()
	r := e.result(id)
	if r.State != "succeeded" || *r.ResponseStatus != 502 || r.Tries != 3 || hits.Load() != 3 {
		t.Fatalf("%+v (hits %d)", r, hits.Load())
	}
}

func TestTimeoutThenSuccess(t *testing.T) {
	e := newExecEnv(t)
	var hits atomic.Int32
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			select { // longer than the response-header timeout
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	e.build()
	id := e.queue(url, nil, nil)
	e.runOne()
	r := e.result(id)
	if r.State != "succeeded" || *r.ResponseStatus != 200 || r.Tries != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestBlockedAtDialNotRetried(t *testing.T) {
	e := newExecEnv(t)
	e.resolver.answers["internal-looking.example.test"] = []netip.Addr{netip.MustParseAddr("10.0.0.8")}
	e.build()
	id := e.queue("https://internal-looking.example.test/hook", nil, nil)
	e.runOne()
	r := e.result(id)
	if r.State != "failed" || *r.FailureCategory != "blocked_address" || r.Tries != 1 || r.Response != nil {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(*r.FailureMessage, "10.0.0.8") {
		t.Fatalf("message leaks the address: %s", *r.FailureMessage)
	}
}

// A URL replay queued while URL replay was on, delivered after it was turned
// off: nothing is sent and the replay fails with url_replay_disabled.
func TestURLReplayRefusedWhenTurnedOff(t *testing.T) {
	e := newExecEnv(t)
	hits := 0
	url := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) { hits++ })
	e.limits.Enabled = false
	e.build()
	id := e.queue(url, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.runner.Run(ctx)
	waitFor(t, 5*time.Second, func() bool {
		return e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1 AND state = 'failed' AND outcome = 'url_replay_disabled'`, id) == 1
	})
	if hits != 0 {
		t.Fatalf("destination reached %d times", hits)
	}
}
