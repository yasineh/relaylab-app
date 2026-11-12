package events

import (
	"context"
	"net/http"
	"testing"
	"time"

	"relaylab/internal/identity/authtest"
)

// These use real Better Auth session rows checked by the same validator the
// REST API uses.

func expectClose(t *testing.T, c *sseConn, event, data string) {
	t.Helper()
	m := c.next(t)
	if m.Event != event || m.Data != data {
		t.Fatalf("frame = %+v, want %s %s", m, event, data)
	}
	c.closed(t, 2*time.Second)
}

func TestStreamClosesWhenSessionExpires(t *testing.T) {
	// The default 60 s revalidation would be too late: the expiry timer acts.
	a := newStreamAPI(t, streamSetup{})
	cookie, _ := authtest.Session(t, a.DB, "alice", time.Now().Add(1500*time.Millisecond))
	c := a.open(t, cookie, a.project, nil)
	c.opened(t)
	start := time.Now()
	expectClose(t, c, "session.expired", `{"reason":"session_ended"}`)
	if waited := time.Since(start); waited > 3*time.Second {
		t.Fatalf("closed after %s", waited)
	}
}

func TestStreamClosesWhenSessionRevoked(t *testing.T) {
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{RevalidateEvery: 200 * time.Millisecond}})
	cookie, token := authtest.Session(t, a.DB, "alice", time.Now().Add(time.Hour))
	c := a.open(t, cookie, a.project, nil)
	c.opened(t)
	// Sign-out (here or on another device) deletes the session row.
	if _, err := a.Admin.Exec(context.Background(), `DELETE FROM auth."session" WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	expectClose(t, c, "session.expired", `{"reason":"session_ended"}`)
}

func TestStreamStaysOpenWhenSessionRefreshed(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	cookie, token := authtest.Session(t, a.DB, "alice", time.Now().Add(time.Second))
	c := a.open(t, cookie, a.project, nil)
	c.opened(t)
	// Better Auth extends expiresAt on use (updateAge).
	if _, err := a.Admin.Exec(context.Background(),
		`UPDATE auth."session" SET "expiresAt" = now() + interval '1 hour' WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // past the original expiry
	id := a.publish(t, a.project, a.ep, "/still-open")
	expectEvents(t, c, id)
}

func TestStreamClosesWhenProjectDeleted(t *testing.T) {
	a := newStreamAPI(t, streamSetup{opts: StreamOptions{RevalidateEvery: 200 * time.Millisecond}})
	c := a.open(t, a.alice, a.project, nil)
	c.opened(t)
	if _, err := a.Admin.Exec(context.Background(), `DELETE FROM app.projects WHERE id = $1`, a.project); err != nil {
		t.Fatal(err)
	}
	expectClose(t, c, "resync.required", `{"reason":"project_not_found"}`)
	if again := a.open(t, a.alice, a.project, nil); again.resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reconnect status %d, want 404", again.resp.StatusCode)
	}
}
