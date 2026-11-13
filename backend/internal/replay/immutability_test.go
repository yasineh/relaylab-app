package replay

import (
	"context"
	"testing"
)

func (e *execEnv) eventSnapshot() string {
	e.t.Helper()
	var s string
	if err := e.Admin.QueryRow(context.Background(),
		`SELECT row_to_json(w)::text FROM app.webhook_events w WHERE id = $1`, e.event).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// TestReplaysNeverChangeTheOriginalEvent: creation, delivery, retries, and
// failure leave every column of the original event as it was.
func TestReplaysNeverChangeTheOriginalEvent(t *testing.T) {
	e := newExecEnv(t)
	e.build()
	before := e.eventSnapshot()

	api := &apiEnv{Env: e.Env, t: t}
	sink := api.endpoint(t, e.project, "Sink", true)
	ok := e.queue("", &sink, []Override{{"X-Mode", "test"}})
	e.runOne()
	// Fails after all tries: the name never resolves (dns_failure is retried).
	failed := e.queue("https://unresolvable.example.test/", nil, nil)
	e.runOne()

	if r := e.result(ok); r.State != "succeeded" {
		t.Fatalf("sink replay %+v", r)
	}
	if r := e.result(failed); r.State != "failed" || r.Tries != e.limits.MaxTries {
		t.Fatalf("failed replay %+v", r)
	}
	if after := e.eventSnapshot(); after != before {
		t.Fatalf("original event changed:\nbefore %s\nafter  %s", before, after)
	}
}
