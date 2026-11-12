package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/dbtest"
)

// listenerPID waits for the broker's LISTEN connection to the test database
// and returns its backend pid, skipping any pid in not.
func listenerPID(t *testing.T, admin *pgx.Conn, dbName string, not int32) int32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var pid int32
		err := admin.QueryRow(context.Background(),
			`SELECT pid FROM pg_stat_activity
			 WHERE application_name = $1 AND datname = $2 AND pid <> $3
			   AND query ILIKE 'LISTEN%'`,
			ListenerApplicationName, dbName, not).Scan(&pid)
		if err == nil {
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("listener connection never appeared")
	return 0
}

func notify(t *testing.T, admin *pgx.Conn, p, e uuid.UUID) {
	t.Helper()
	payload := `{"project_id":"` + p.String() + `","event_id":"` + e.String() + `"}`
	if _, err := admin.Exec(context.Background(), "SELECT pg_notify('events', $1)", payload); err != nil {
		t.Fatal(err)
	}
}

func TestListenerReconnectRequiresResync(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, d.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()

	src, err := NewPGSource(config.NewSecret(d.RuntimeURL), nil)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBroker(Options{Source: src, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { b.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()

	pid := listenerPID(t, admin, d.Name, 0)

	// Notifications from the database reach subscribers of that project only.
	p, other := uuid.New(), uuid.New()
	sub, otherSub := b.Subscribe(p), b.Subscribe(other)
	id := uuid.Must(uuid.NewV7())
	notify(t, admin, p, id)
	if got := recv(t, sub); got != id {
		t.Fatalf("got %s, want %s", got, id)
	}
	assertNothing(t, otherSub)

	// Kill the listening backend: notifications sent while it is down are
	// lost, so every subscriber must be told to resync.
	if _, err := admin.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Subscription{sub, otherSub} {
		select {
		case <-s.Dropped():
		case <-time.After(10 * time.Second):
			t.Fatal("subscriber not told to resync after the listener was killed")
		}
	}

	// The broker reconnects on a new backend and delivers again.
	listenerPID(t, admin, d.Name, pid)
	again := b.Subscribe(p)
	defer again.Close()
	id2 := uuid.Must(uuid.NewV7())
	notify(t, admin, p, id2)
	if got := recv(t, again); got != id2 {
		t.Fatalf("after reconnect got %s, want %s", got, id2)
	}
}
