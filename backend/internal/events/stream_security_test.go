package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestStreamInjectionYieldsOneMessage: received content that looks like SSE
// framing (newlines, carriage returns, field names) stays inside one JSON
// data line and never produces another event, id, or retry field.
func TestStreamInjectionYieldsOneMessage(t *testing.T) {
	a := newStreamAPI(t, streamSetup{})
	evilName := "Stripe\r\nevent: x\n\nid: 1"
	if _, err := a.Admin.Exec(context.Background(), `UPDATE app.endpoints SET name = $1 WHERE id = $2`, evilName, a.ep); err != nil {
		t.Fatal(err)
	}
	c := a.open(t, a.alice, a.project, nil)
	c.opened(t)

	paths := []string{
		"/hooks\n\nevent: x",
		"/hooks\r\n\r\nevent: x\r\ndata: {}\r\n",
		"/hooks\rretry: 1\rid: 2",
		"/hooks event: x data: y",
		"/hooks\x1b[31m: comment\n:", // (text columns cannot hold NUL)
	}
	for _, p := range paths {
		id := a.publish(t, a.project, a.ep, p)
		m := c.next(t)
		if m.Event != "event.created" || m.ID != id.String() || m.Lines != 3 || m.Retry != "" {
			t.Fatalf("path %q produced frame %+v", p, m)
		}
		if strings.ContainsAny(m.Data, "\r\n") {
			t.Fatalf("raw line break in data: %q", m.Data)
		}
		var got summaryJSON
		if err := json.Unmarshal([]byte(m.Data), &got); err != nil {
			t.Fatalf("data for %q is not one JSON value: %v", p, err)
		}
		if got.Path != p || got.EndpointName != evilName {
			t.Fatalf("content altered: path %q name %q", got.Path, got.EndpointName)
		}
	}
	// Nothing else was produced: the next frame is the next real event.
	sentinel := a.publish(t, a.project, a.ep, "/sentinel")
	select {
	case m := <-c.msgs:
		if m.Comment == "" && m.ID != sentinel.String() {
			t.Fatalf("extra frame: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sentinel not delivered")
	}
}
