package replay

import (
	"context"
	"sync/atomic"
	"testing"

	"relaylab/internal/platform/db/store"
	"relaylab/internal/testutil/fixture"
)

type guardFlag struct{ active atomic.Bool }

func (g *guardFlag) Active() bool { return g.active.Load() }

// TestHeartbeatDrivesAPIGate: what the worker reports is what the API's gate
// sees.
func TestHeartbeatDrivesAPIGate(t *testing.T) {
	env := fixture.New(t)
	ctx := context.Background()
	guard := &guardFlag{}
	hb := NewHeartbeat(env.Pool, "worker-1", "rev", guard, nil)
	gate := NewGate(true, store.New(env.Pool))

	if err := hb.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := gate.URLAvailable(ctx); ok {
		t.Fatal("inactive guard reported available")
	}
	guard.active.Store(true)
	if err := hb.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := gate.URLAvailable(ctx); err != nil || !ok {
		t.Fatalf("active guard: %v %v", ok, err)
	}
	if env.Count(t, `SELECT count(*) FROM app.worker_heartbeats`) != 1 {
		t.Fatal("heartbeat rows not upserted")
	}
	// A heartbeat older than the window no longer counts.
	if _, err := env.Admin.Exec(ctx, `UPDATE app.worker_heartbeats SET seen_at = now() - interval '61 seconds'`); err != nil {
		t.Fatal(err)
	}
	if ok, _ := gate.URLAvailable(ctx); ok {
		t.Fatal("stale heartbeat reported available")
	}
	// Rows of workers gone for a day are removed by the next beat.
	if _, err := env.Admin.Exec(ctx, `INSERT INTO app.worker_heartbeats (worker_id, outbound_guard, version, seen_at) VALUES ('old', 'active', 'v', now() - interval '2 days')`); err != nil {
		t.Fatal(err)
	}
	if err := hb.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if env.Count(t, `SELECT count(*) FROM app.worker_heartbeats WHERE worker_id = 'old'`) != 0 {
		t.Fatal("stale worker row kept")
	}
}
