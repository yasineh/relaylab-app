package replay

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// secondWorker returns another runner and deliverer sharing e's guard and
// database, with a different worker id.
func (e *execEnv) secondWorker(id string) (*Runner, *Deliverer) {
	d := NewDeliverer(DelivererOptions{
		Pool: e.Pool, Sender: e.deliverer.sender, Sealer: e.sealer, Red: red, Limits: e.limits,
		Backoff: []time.Duration{10 * time.Millisecond},
	})
	r := NewRunner(RunnerOptions{Pool: e.Pool, WorkerID: id, Limits: e.limits, Slots: 4, Executor: d})
	d.Bind(r)
	return r, d
}

func TestLostLeaseMidRunHasOneFinalResult(t *testing.T) {
	e := newExecEnv(t)
	e.opts.ResponseHeaderTimeout = 5 * time.Second
	e.opts.TotalTimeout = 10 * time.Second
	var hits atomic.Int32
	release := make(chan struct{})
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			<-release // worker A's request hangs: A is "dead"
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	e.build()
	id := e.queue(url, nil, nil)

	// Worker A claims and starts delivering.
	jobA, ok, err := e.runner.Claim(context.Background())
	if err != nil || !ok {
		t.Fatal(err)
	}
	doneA := make(chan struct{})
	go func() { e.deliverer.Execute(context.Background(), jobA); close(doneA) }()
	waitFor(t, 5*time.Second, func() bool { return hits.Load() == 1 })

	// A's lease expires; worker B reaps, reclaims, and finishes.
	if _, err := e.Admin.Exec(context.Background(), `UPDATE app.jobs SET lease_expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	runnerB, delivererB := e.secondWorker("w2")
	if err := runnerB.Reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobB, ok, err := runnerB.Claim(context.Background())
	if err != nil || !ok || jobB.Attempts != 2 {
		t.Fatalf("reclaim: %v %v attempts %d", ok, err, jobB.Attempts)
	}
	delivererB.Execute(context.Background(), jobB)
	if r := e.result(id); r.State != "succeeded" || *r.ResponseStatus != 200 {
		t.Fatalf("after B: %+v", r)
	}

	// A wakes up with a response, but its lease is gone: nothing is written.
	close(release)
	<-doneA
	r := e.result(id)
	if r.State != "succeeded" || *r.ResponseStatus != 200 || r.Tries != 1 {
		t.Fatalf("final result overwritten by the stale worker: %+v", r)
	}
	if e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'done' AND attempts = 2`) != 1 {
		t.Fatal("job not done after reclaim")
	}
	if hits.Load() != 2 {
		t.Fatalf("destination hits %d (at-least-once: 2 expected here)", hits.Load())
	}
}
