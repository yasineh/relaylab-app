package events

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/identity/authtest"
	"relaylab/internal/platform/config"
	"relaylab/internal/realtime"
)

// openN opens n streams for cookie across the given projects in turn.
func (a *streamAPI) openN(t *testing.T, cookie string, n int, projects ...uuid.UUID) []*sseConn {
	t.Helper()
	conns := make([]*sseConn, n)
	for i := range n {
		conns[i] = a.open(t, cookie, projects[i%len(projects)], nil)
		if conns[i].resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: status %d", i+1, conns[i].resp.StatusCode)
		}
		conns[i].opened(t)
	}
	return conns
}

func assertTooMany(t *testing.T, c *sseConn) {
	t.Helper()
	if c.resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", c.resp.StatusCode)
	}
	if code := errCode(t, c.resp); code != "too_many_streams" {
		t.Fatalf("code %q", code)
	}
}

func TestStreamLimitDefault(t *testing.T) {
	limit := config.DefaultLimits().StreamsPerUser
	if limit != 5 {
		t.Fatalf("LIMIT_STREAMS_PER_USER default = %d, spec says 5", limit)
	}
	a := newStreamAPI(t, streamSetup{})
	a.openN(t, a.alice, limit, a.project)
	assertTooMany(t, a.open(t, a.alice, a.project, nil))
}

func TestStreamSlotFreedOnDisconnect(t *testing.T) {
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{Slots: realtime.NewSlots(2)}})
	conns := a.openN(t, a.alice, 2, a.project)
	assertTooMany(t, a.open(t, a.alice, a.project, nil))

	// The browser closes one stream; the server notices and frees its slot.
	_ = conns[0].resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c := a.open(t, a.alice, a.project, nil)
		if c.resp.StatusCode == http.StatusOK {
			c.opened(t)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not freed: status %d", c.resp.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStreamLimitIsPerUserNotPerProject(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	limit := config.DefaultLimits().StreamsPerUser
	second := a.Project(t, "alice", "Second")
	a.openN(t, a.alice, limit, a.project, second)
	assertTooMany(t, a.open(t, a.alice, second, nil))
	assertTooMany(t, a.open(t, a.alice, a.project, nil))

	// Bob is unaffected, and each of alice's sessions counts toward her cap.
	bobs := a.Project(t, "bob", "Bob's")
	bob := authtest.SessionCookie(t, a.DB, "bob")
	a.openN(t, bob, 1, bobs)
	other := authtest.SessionCookie(t, a.DB, "alice")
	assertTooMany(t, a.open(t, other, a.project, nil))
}

func TestStreamLimitConfigured(t *testing.T) {
	l, err := config.LoadLimits(func(k string) string {
		if k == "LIMIT_STREAMS_PER_USER" {
			return "2"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{Slots: realtime.NewSlots(l.StreamsPerUser)}})
	a.openN(t, a.alice, 2, a.project)
	assertTooMany(t, a.open(t, a.alice, a.project, nil))
}

// Rejected streams never subscribe or hold a slot.
func TestStreamRejectionHoldsNothing(t *testing.T) {
	slots := realtime.NewSlots(1)
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{Slots: slots}})
	a.openN(t, a.alice, 1, a.project)
	assertTooMany(t, a.open(t, a.alice, a.project, nil))
	if n := a.broker.Subscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
	if n := slots.Open("alice"); n != 1 {
		t.Fatalf("slots = %d, want 1", n)
	}
}
