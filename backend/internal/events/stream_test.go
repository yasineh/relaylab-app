package events

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/redact"
	"relaylab/internal/realtime"
)

// streamAPI serves the event routes, with the live stream, over a real HTTP
// server, with a broker listening on the test database.
type streamAPI struct {
	*seedEnv
	srv    *httptest.Server
	broker *realtime.Broker
	ep     uuid.UUID
	alice  string // session cookie
}

type streamSetup struct {
	opts      StreamOptions
	configure func(*http.Server) // adjust server timeouts before start
}

func newStreamAPI(t *testing.T, setup streamSetup) *streamAPI {
	t.Helper()
	e := newSeedEnv(t)
	src, err := realtime.NewPGSource(config.NewSecret(e.DB.RuntimeURL), nil)
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 1)
	b := realtime.NewBroker(realtime.Options{
		Source: src, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		OnConnect: func() {
			select {
			case connected <- struct{}{}:
			default:
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("broker never connected")
	}

	o := setup.opts
	o.Broker = b
	if o.Slots == nil {
		o.Slots = realtime.NewSlots(config.DefaultLimits().StreamsPerUser)
	}
	if o.Validator == nil {
		o.Validator = e.Validator()
	}
	svc := NewService(e.Pool, redact.New(nil, nil))
	h := e.Router(func(v1 chi.Router) {
		v1.Route("/projects/{projectId}/events", NewHandler(svc, 50, 100).WithStream(o).Routes)
	})
	srv := httptest.NewUnstartedServer(h)
	if setup.configure != nil {
		setup.configure(srv.Config)
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return &streamAPI{
		seedEnv: e, srv: srv, broker: b,
		ep:    e.endpoint(t, e.project, "Stripe"),
		alice: authtest.SessionCookie(t, e.DB, "alice"),
	}
}

func (a *streamAPI) url(project uuid.UUID) string {
	return a.srv.URL + "/api/v1/projects/" + project.String() + "/events/stream"
}

// publish stores an event and notifies, as ingestion does.
func (a *streamAPI) publish(t *testing.T, project, endpoint uuid.UUID, path string) uuid.UUID {
	t.Helper()
	id := idAt(t, time.Now().UTC())
	if _, err := a.Admin.Exec(context.Background(), `
		INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, query_params,
		  headers, content_type, body, body_size, body_encoding, correlation_id)
		VALUES ($1, $2, $3, now(), 'POST', $4, '{"token":["q-secret"]}', '[["X-Secret","h-secret"]]',
		  'application/json', '\x7b22626f6479223a2262732d736563726574227d', 20, 'utf8', 'test')`,
		id, endpoint, project, path); err != nil {
		t.Fatal(err)
	}
	a.notify(t, project, id)
	return id
}

func (a *streamAPI) notify(t *testing.T, project, id uuid.UUID) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"project_id": project.String(), "event_id": id.String()})
	if _, err := a.Admin.Exec(context.Background(), "SELECT pg_notify('events', $1)", string(payload)); err != nil {
		t.Fatal(err)
	}
}

// sseMsg is one parsed SSE frame. Comment frames have only Comment set.
type sseMsg struct {
	ID, Event, Data, Retry, Comment string
	Lines                           int // field lines in the frame
}

type sseConn struct {
	resp *http.Response
	msgs chan sseMsg
	done chan struct{} // closed when the server ends the stream
}

// open starts a stream request. The response is returned for status checks;
// for 200 responses, frames arrive on msgs.
func (a *streamAPI) open(t *testing.T, cookie string, project uuid.UUID, header http.Header) *sseConn {
	t.Helper()
	return a.openURL(t, cookie, a.url(project), header)
}

func (a *streamAPI) openURL(t *testing.T, cookie, url string, header http.Header) *sseConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "text/event-stream")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	c := &sseConn{resp: resp, msgs: make(chan sseMsg, 1024), done: make(chan struct{})}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		close(c.done)
		return c
	}
	go func() {
		defer close(c.done)
		sc := bufio.NewScanner(resp.Body)
		var m sseMsg
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if m != (sseMsg{}) {
					c.msgs <- m
				}
				m = sseMsg{}
				continue
			case strings.HasPrefix(line, ":"):
				m.Comment = strings.TrimSpace(line[1:])
				continue
			}
			m.Lines++
			field, value, _ := strings.Cut(line, ": ")
			switch field {
			case "id":
				m.ID = value
			case "event":
				m.Event = value
			case "data":
				m.Data += value
			case "retry":
				m.Retry = value
			default:
				m.Event = "UNEXPECTED FIELD " + field
			}
		}
	}()
	return c
}

// next returns the next frame that is not a heartbeat comment.
func (c *sseConn) next(t *testing.T) sseMsg {
	t.Helper()
	for {
		select {
		case m := <-c.msgs:
			if m.Comment != "" {
				continue
			}
			return m
		case <-c.done:
			select {
			case m := <-c.msgs:
				if m.Comment == "" {
					return m
				}
			default:
			}
			t.Fatal("stream ended")
		case <-time.After(5 * time.Second):
			t.Fatal("no stream message within 5s")
		}
	}
}

// opened waits for the retry frame every stream starts with.
func (c *sseConn) opened(t *testing.T) {
	t.Helper()
	if m := c.next(t); m.Retry != "3000" || m.Lines != 1 {
		t.Fatalf("first frame = %+v, want retry: 3000", m)
	}
}

func (c *sseConn) closed(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(within):
		t.Fatalf("stream still open after %s", within)
	}
}

func errCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var b errBody
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return b.Error.Code
}

func TestStreamRequiresValidSession(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	expired, _ := authtest.Session(t, a.DB, "alice", time.Now().Add(-time.Minute))
	revoked, token := authtest.Session(t, a.DB, "alice", time.Now().Add(time.Hour))
	if _, err := a.Admin.Exec(context.Background(), `DELETE FROM auth."session" WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	// Same token, signature from a different secret.
	tampered := a.alice[:len(a.alice)-6] + "AAAA%3D"

	for _, tc := range []struct{ name, cookie, code string }{
		{"missing", "", "unauthenticated"},
		{"invalid signature", tampered, "unauthenticated"},
		{"expired", expired, "session_expired"},
		{"revoked", revoked, "unauthenticated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := a.open(t, tc.cookie, a.project, nil)
			if c.resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", c.resp.StatusCode)
			}
			if ct := c.resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				t.Fatalf("a stream was opened: %s", ct)
			}
			if code := errCode(t, c.resp); code != tc.code {
				t.Fatalf("code %q, want %q", code, tc.code)
			}
		})
	}
	if n := a.broker.Subscribers(); n != 0 {
		t.Fatalf("rejected requests left %d subscribers", n)
	}
}

func TestStreamForeignOrDeletedProjectIsNotFound(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	bob := authtest.SessionCookie(t, a.DB, "bob")
	deleted := a.Project(t, "alice", "Gone")
	if _, err := a.Admin.Exec(context.Background(), `DELETE FROM app.projects WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		cookie  string
		project uuid.UUID
	}{
		{"other user's project", bob, a.project},
		{"deleted project", a.alice, deleted},
		{"unknown project", a.alice, uuid.New()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := a.open(t, tc.cookie, tc.project, nil)
			if c.resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d, want 404", c.resp.StatusCode)
			}
			if code := errCode(t, c.resp); code != "not_found" {
				t.Fatalf("code %q", code)
			}
		})
	}
	if n := a.broker.Subscribers(); n != 0 {
		t.Fatalf("rejected requests left %d subscribers", n)
	}
}

func TestStreamFramingAndDelivery(t *testing.T) {
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{Heartbeat: 50 * time.Millisecond}})
	other := a.Project(t, "alice", "Other")
	otherEP := a.endpoint(t, other, "Other endpoint")

	c := a.open(t, a.alice, a.project, nil)
	if c.resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", c.resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-store", "X-Accel-Buffering": "no",
	} {
		if got := c.resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := c.resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS header sent: %q", got)
	}
	c.opened(t)

	// An event in another project is not delivered; one in this project is.
	a.publish(t, other, otherEP, "/elsewhere")
	start := time.Now()
	id := a.publish(t, a.project, a.ep, "/hooks/stripe")
	m := c.next(t)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("delivered after %s, want under 2s", elapsed)
	}
	if m.Event != "event.created" || m.ID != id.String() || m.Lines != 3 {
		t.Fatalf("frame = %+v", m)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(m.Data), &got); err != nil {
		t.Fatalf("data is not JSON: %v", err)
	}
	want := map[string]any{
		"id": id.String(), "endpoint_id": a.ep.String(), "endpoint_name": "Stripe", "method": "POST",
		"path": "/hooks/stripe", "content_type": "application/json", "body_size": float64(20),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("data[%s] = %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["received_at"].(string); !ok || len(got) != len(want)+1 {
		t.Errorf("data has unexpected fields: %v", got)
	}
	// Only summary fields: no body, header, or query values.
	for _, secret := range []string{"q-secret", "h-secret", "bs-secret"} {
		if strings.Contains(m.Data, secret) {
			t.Errorf("stream message contains %q", secret)
		}
	}

	// Idle streams get heartbeat comments.
	deadline := time.After(2 * time.Second)
	for pings := 0; pings < 2; {
		select {
		case m := <-c.msgs:
			if m.Comment == "ping" {
				pings++
			} else {
				t.Fatalf("unexpected frame while idle: %+v", m)
			}
		case <-deadline:
			t.Fatal("no heartbeats")
		}
	}
}

// TestStreamOutlivesServerTimeouts: the stream sets a deadline per write, so
// it stays open past the short read and write timeouts every other route
// keeps. Without the per-write deadline the server's WriteTimeout ends it.
func TestStreamOutlivesServerTimeouts(t *testing.T) {
	a := newStreamAPI(t, streamSetup{
		opts: StreamOptions{Heartbeat: 100 * time.Millisecond},
		configure: func(s *http.Server) {
			s.ReadTimeout = 300 * time.Millisecond
			s.WriteTimeout = 300 * time.Millisecond
		},
	})
	c := a.open(t, a.alice, a.project, nil)
	c.opened(t)
	time.Sleep(time.Second)
	id := a.publish(t, a.project, a.ep, "/late")
	if m := c.next(t); m.ID != id.String() {
		t.Fatalf("frame = %+v", m)
	}
}

func TestStreamEndsOnShutdown(t *testing.T) {
	shutdown := make(chan struct{})
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{Shutdown: shutdown}})
	c := a.open(t, a.alice, a.project, nil)
	c.opened(t)
	close(shutdown)
	c.closed(t, 2*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for a.broker.Subscribers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := a.broker.Subscribers(); n != 0 {
		t.Fatalf("%d subscribers left after shutdown", n)
	}
}

func TestSeenSetIsBounded(t *testing.T) {
	s := newSeenSet()
	first := uuid.New()
	s.add(first)
	for range seenSetSize - 1 {
		s.add(uuid.New())
	}
	if !s.has(first) {
		t.Fatal("evicted too early")
	}
	s.add(uuid.New())
	if s.has(first) || len(s.m) != seenSetSize {
		t.Fatalf("oldest id not evicted (size %d)", len(s.m))
	}
}
