package replay

import (
	"context"
	"net/http"
	"testing"

	"relaylab/internal/projects"
)

// TestProjectDeletionRemovesReplaysAndJobs: queued and running replay jobs
// go with their project, in the same transaction as the owner's delete.
func TestProjectDeletionRemovesReplaysAndJobs(t *testing.T) {
	e := newAPIEnv(t, apiOpts{})
	for range 4 {
		if rec := e.alice.Do(http.MethodPost, e.eventPath(e.event), body(sinkDest(e.sink))); rec.Code != 202 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	// One of them is running under a worker's lease.
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.jobs SET state = 'running', lease_owner = 'w1', lease_expires_at = now() + interval '1 minute', attempts = 1
		WHERE id = (SELECT id FROM app.jobs ORDER BY id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.replay_attempts SET state = 'running'
		WHERE id = (SELECT id FROM app.replay_attempts ORDER BY id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'queued'`) != 3 || e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'running'`) != 1 {
		t.Fatal("setup")
	}
	if err := projects.NewService(e.Pool, 20).Delete(context.Background(), "alice", e.project); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"app.replay_attempts", "app.jobs", "app.audit_log", "app.webhook_events"} {
		if n := e.Count(t, "SELECT count(*) FROM "+table+" WHERE project_id = $1", e.project); n != 0 {
			t.Errorf("%d rows left in %s", n, table)
		}
	}
}
