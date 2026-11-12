package realtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeSource is a Source driven by the test: each Listen call connects, then
// delivers from notes until the test fails the connection or ctx ends.
type fakeSource struct {
	notes     chan Notification
	fail      chan error
	connected chan struct{}
	listens   atomic.Int32
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		notes:     make(chan Notification),
		fail:      make(chan error),
		connected: make(chan struct{}, 8),
	}
}

func (f *fakeSource) Listen(ctx context.Context, connected func(), deliver func(Notification)) error {
	f.listens.Add(1)
	connected()
	f.connected <- struct{}{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-f.fail:
			return err
		case n := <-f.notes:
			deliver(n)
		}
	}
}

func startBroker(t *testing.T, o Options) (*Broker, *fakeSource) {
	t.Helper()
	src := newFakeSource()
	o.Source = src
	o.MinBackoff, o.MaxBackoff = time.Millisecond, 5*time.Millisecond
	b := NewBroker(o)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitConnected(t, src)
	return b, src
}

func waitConnected(t *testing.T, src *fakeSource) {
	t.Helper()
	select {
	case <-src.connected:
	case <-time.After(2 * time.Second):
		t.Fatal("source never connected")
	}
}

func recv(t *testing.T, s *Subscription) uuid.UUID {
	t.Helper()
	select {
	case id := <-s.Events():
		return id
	case <-s.Dropped():
		t.Fatal("subscription dropped")
	case <-time.After(2 * time.Second):
		t.Fatal("no event delivered")
	}
	return uuid.Nil
}

func assertNothing(t *testing.T, s *Subscription) {
	t.Helper()
	select {
	case id := <-s.Events():
		t.Fatalf("unexpected event %s", id)
	case <-s.Dropped():
		t.Fatal("unexpected drop")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestFanOutToAllProjectSubscribers(t *testing.T) {
	b, src := startBroker(t, Options{})
	p := uuid.New()
	s1, s2 := b.Subscribe(p), b.Subscribe(p)
	defer s1.Close()
	defer s2.Close()

	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		src.notes <- Notification{ProjectID: p, EventID: id}
	}
	for _, s := range []*Subscription{s1, s2} {
		for _, want := range ids {
			if got := recv(t, s); got != want {
				t.Fatalf("got %s, want %s (order must be preserved)", got, want)
			}
		}
	}
}

func TestIsolationByProject(t *testing.T) {
	b, src := startBroker(t, Options{})
	pa, pb := uuid.New(), uuid.New()
	sa, sb := b.Subscribe(pa), b.Subscribe(pb)
	defer sa.Close()
	defer sb.Close()

	id := uuid.New()
	src.notes <- Notification{ProjectID: pa, EventID: id}
	if got := recv(t, sa); got != id {
		t.Fatalf("got %s", got)
	}
	assertNothing(t, sb)
}

func TestSlowSubscriberDroppedWithoutBlocking(t *testing.T) {
	var slow atomic.Int32
	b, src := startBroker(t, Options{OnSlowDrop: func() { slow.Add(1) }})
	p := uuid.New()
	stuck := b.Subscribe(p) // never reads
	live := b.Subscribe(p)
	defer stuck.Close()
	defer live.Close()

	// The live reader drains concurrently; delivery to it must keep going
	// while the stuck one fills up and is dropped.
	var got sync.WaitGroup
	got.Add(1)
	total := SubscriberBuffer + 10
	go func() {
		defer got.Done()
		for range total {
			select {
			case <-live.Events():
			case <-time.After(2 * time.Second):
				t.Error("live subscriber starved")
				return
			}
		}
	}()
	for range total {
		select {
		case src.notes <- Notification{ProjectID: p, EventID: uuid.New()}:
		case <-time.After(2 * time.Second):
			t.Fatal("dispatch blocked on a slow subscriber")
		}
	}
	got.Wait()

	select {
	case <-stuck.Dropped():
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not dropped")
	}
	select {
	case <-live.Dropped():
		t.Fatal("reading subscriber was dropped")
	default:
	}
	if n := slow.Load(); n != 1 {
		t.Fatalf("OnSlowDrop called %d times, want 1", n)
	}
	if n := b.Subscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
}

func TestReconnectDropsAllSubscribersForResync(t *testing.T) {
	b, src := startBroker(t, Options{})
	s1, s2 := b.Subscribe(uuid.New()), b.Subscribe(uuid.New())
	src.fail <- errors.New("connection reset")
	waitConnected(t, src)
	for _, s := range []*Subscription{s1, s2} {
		select {
		case <-s.Dropped():
		case <-time.After(time.Second):
			t.Fatal("subscriber not dropped after reconnect")
		}
	}
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("subscribers = %d after reconnect", n)
	}
	if n := src.listens.Load(); n != 2 {
		t.Fatalf("listens = %d, want 2", n)
	}
	// New subscribers work on the new connection.
	p := uuid.New()
	s3 := b.Subscribe(p)
	defer s3.Close()
	id := uuid.New()
	src.notes <- Notification{ProjectID: p, EventID: id}
	if got := recv(t, s3); got != id {
		t.Fatalf("got %s", got)
	}
}

func TestCloseUnsubscribesAndIsIdempotent(t *testing.T) {
	b, src := startBroker(t, Options{})
	p := uuid.New()
	s := b.Subscribe(p)
	s.Close()
	s.Close()
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("subscribers = %d", n)
	}
	src.notes <- Notification{ProjectID: p, EventID: uuid.New()}
	assertNothing(t, s)
}

func TestParsePayload(t *testing.T) {
	p, e := uuid.New(), uuid.New()
	good := `{"project_id":"` + p.String() + `","event_id":"` + e.String() + `"}`
	if n, ok := parsePayload(good); !ok || n.ProjectID != p || n.EventID != e {
		t.Fatalf("good payload: %+v %v", n, ok)
	}
	for _, bad := range []string{
		"", "{", `{"project_id":"x","event_id":"y"}`,
		`{"project_id":"` + p.String() + `"}`,
		`{"project_id":"00000000-0000-0000-0000-000000000000","event_id":"` + e.String() + `"}`,
	} {
		if _, ok := parsePayload(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
