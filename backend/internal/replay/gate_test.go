package replay

import (
	"context"
	"errors"
	"sync"
	"testing"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/testutil/fixture"
)

type fakeStatus struct {
	mu     sync.Mutex
	active bool
	err    error
	window int32
}

func (f *fakeStatus) GuardActiveRecently(_ context.Context, w int32) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.window = w
	return f.active, f.err
}

func (f *fakeStatus) setActive(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = v
}

func TestGate(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		enabled   bool
		status    *fakeStatus
		url, sink error
	}{
		{"enabled, guard active", true, &fakeStatus{active: true}, nil, nil},
		{"enabled, guard inactive or stale", true, &fakeStatus{}, ErrUnavailable, nil},
		{"disabled", false, &fakeStatus{active: true}, ErrUnavailable, ErrUnavailable},
	} {
		g := NewGate(tc.enabled, tc.status)
		if err := g.Check(ctx, DestinationURL); !errors.Is(err, tc.url) {
			t.Errorf("%s: url %v, want %v", tc.name, err, tc.url)
		}
		if err := g.Check(ctx, DestinationSink); !errors.Is(err, tc.sink) {
			t.Errorf("%s: sink %v, want %v", tc.name, err, tc.sink)
		}
		if tc.enabled && tc.status.window != 60 {
			t.Errorf("%s: window %d s", tc.name, tc.status.window)
		}
	}
	// A heartbeat read error fails closed for URLs and is reported.
	g := NewGate(true, &fakeStatus{active: true, err: errors.New("db down")})
	if err := g.Check(ctx, DestinationURL); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("read error: %v", err)
	}
	if err := g.Check(ctx, DestinationSink); err != nil {
		t.Errorf("sink with heartbeat error: %v", err)
	}
}

// TestGateReadsHeartbeats: the real heartbeat query (recent active, stale
// active, recent inactive).
func TestGateReadsHeartbeats(t *testing.T) {
	env := fixture.New(t)
	ctx := context.Background()
	g := NewGate(true, store.New(env.Pool))
	set := func(sql string) {
		t.Helper()
		if _, err := env.Admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := g.URLAvailable(ctx); err != nil || ok {
		t.Fatalf("no heartbeats: %v %v", ok, err)
	}
	set(`INSERT INTO app.worker_heartbeats (worker_id, outbound_guard, version, seen_at) VALUES ('w1', 'inactive', 'v', now())`)
	if ok, _ := g.URLAvailable(ctx); ok {
		t.Fatal("inactive guard reported available")
	}
	set(`INSERT INTO app.worker_heartbeats (worker_id, outbound_guard, version, seen_at) VALUES ('w2', 'active', 'v', now() - interval '2 minutes')`)
	if ok, _ := g.URLAvailable(ctx); ok {
		t.Fatal("stale heartbeat reported available")
	}
	set(`UPDATE app.worker_heartbeats SET seen_at = now() - interval '30 seconds' WHERE worker_id = 'w2'`)
	if ok, err := g.URLAvailable(ctx); err != nil || !ok {
		t.Fatalf("recent active heartbeat: %v %v", ok, err)
	}
}
