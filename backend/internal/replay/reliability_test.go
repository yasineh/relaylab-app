package replay

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"relaylab/internal/testutil/fixture"
)

func TestTransientFailureThenSuccess(t *testing.T) {
	e := newExecEnv(t)
	var hits atomic.Int32
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	e.build()
	id := e.queue(url, nil, nil)
	e.runOne()
	if r := e.result(id); r.State != "succeeded" || *r.ResponseStatus != 201 || r.Tries != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestDeadAfterMaxLeaseLosses(t *testing.T) {
	e := newExecEnv(t)
	reg := prometheus.NewRegistry()
	e.metrics = NewMetrics(reg)
	e.build()
	id := e.queue("https://example.com/", nil, nil)
	expire := func() {
		if _, err := e.Admin.Exec(context.Background(), `UPDATE app.jobs SET lease_expires_at = now() - interval '1 second' WHERE state = 'running'`); err != nil {
			t.Fatal(err)
		}
	}
	for loss := 1; loss <= e.limits.MaxLeaseLosses; loss++ {
		job, ok, err := e.runner.Claim(context.Background())
		if err != nil || !ok || job.Attempts != loss {
			t.Fatalf("claim %d: %v %v attempts %d", loss, ok, err, job.Attempts)
		}
		expire() // the worker holding it stops renewing (crashed)
		if err := e.runner.Reap(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'dead' AND last_error = 'lease_expired'`) != 1 {
		t.Fatal("job not dead")
	}
	r := e.result(id)
	if r.State != "failed" || *r.FailureCategory != CategoryWorkerLost || r.FailureMessage == nil {
		t.Fatalf("%+v", r)
	}
	if got := testutil.ToFloat64(e.metrics.dead); got != 1 {
		t.Fatalf("jobs_dead_total = %v", got)
	}
	if _, ok, _ := e.runner.Claim(context.Background()); ok {
		t.Fatal("dead job claimed again")
	}
}

func TestAbandonedLeaseRecovered(t *testing.T) {
	e := newExecEnv(t)
	var hits atomic.Int32
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		hits.Add(1)
	})
	e.build()
	id := e.queue(url, nil, nil)
	if _, ok, err := e.runner.Claim(context.Background()); err != nil || !ok {
		t.Fatal(err)
	} // ...and the claiming worker vanishes without delivering.
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.jobs SET lease_expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	runnerB, delivererB := e.secondWorker("w2")
	if err := runnerB.Reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, ok, err := runnerB.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("reclaim: %v %v", ok, err)
	}
	delivererB.Execute(context.Background(), job)
	if r := e.result(id); r.State != "succeeded" || hits.Load() != 1 {
		t.Fatalf("%+v hits %d", r, hits.Load())
	}
}

// TestFourRunnersFiftyJobsExactlyOnce: concurrent claims never run a job
// twice.
func TestFourRunnersFiftyJobsExactlyOnce(t *testing.T) {
	env := fixture.New(t, "alice")
	var all []uuid.UUID
	for i := range 10 {
		p := env.Project(t, "alice", "P"+string(rune('a'+i)))
		all = append(all, queueJobs(t, env, p, 5)...)
	}
	rec := newRecorder(5 * time.Millisecond)
	limits := testLimits()
	for _, w := range []string{"w1", "w2", "w3", "w4"} {
		startRunner(t, env, w, rec, limits, 8)
	}
	waitFor(t, 20*time.Second, func() bool {
		return env.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'done'`) == len(all)
	})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.runs) != len(all) {
		t.Fatalf("%d distinct jobs ran, want %d", len(rec.runs), len(all))
	}
	for _, id := range all {
		if rec.runs[id] != 1 {
			t.Errorf("job %s ran %d times", id, rec.runs[id])
		}
	}
	for p, peak := range rec.peak {
		if peak > limits.ProjectConcurrency {
			t.Errorf("project %s peaked at %d running", p, peak)
		}
	}
}
