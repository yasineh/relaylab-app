package replay

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/testutil/fixture"
)

// recorder is an Executor that records executions and the most jobs of one
// project running at the same moment, across every runner sharing it.
type recorder struct {
	mu      sync.Mutex
	runs    map[uuid.UUID]int
	running map[uuid.UUID]int
	peak    map[uuid.UUID]int
	hold    time.Duration
	runners map[string]*Runner
}

func newRecorder(hold time.Duration) *recorder {
	return &recorder{runs: map[uuid.UUID]int{}, running: map[uuid.UUID]int{}, peak: map[uuid.UUID]int{}, hold: hold, runners: map[string]*Runner{}}
}

func (rc *recorder) Execute(ctx context.Context, job Job) {
	rc.mu.Lock()
	rc.runs[job.ID]++
	rc.running[job.ProjectID]++
	rc.peak[job.ProjectID] = max(rc.peak[job.ProjectID], rc.running[job.ProjectID])
	rc.mu.Unlock()
	sleep(ctx, rc.hold)
	rc.mu.Lock()
	rc.running[job.ProjectID]--
	rc.mu.Unlock()
	_, _ = job.runner.Finish(ctx, store.New(job.runner.pool), job, "done", "")
}

func testLimits() *config.Replay {
	return &config.Replay{ProjectConcurrency: 2, ProjectQueueDepth: 20, MaxTries: 3, LeaseSeconds: 60, MaxLeaseLosses: 3, ResponseExcerptBytes: 64 << 10}
}

// queueJobs inserts n queued replay.deliver jobs for project.
func queueJobs(t *testing.T, env *fixture.Env, project uuid.UUID, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.Must(uuid.NewV7())
		payload, _ := json.Marshal(map[string]string{"replay_id": uuid.NewString()})
		if _, err := env.Admin.Exec(context.Background(),
			`INSERT INTO app.jobs (id, kind, project_id, payload, max_attempts) VALUES ($1, 'replay.deliver', $2, $3, 3)`,
			ids[i], project, payload); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func startRunner(t *testing.T, env *fixture.Env, id string, exec Executor, limits *config.Replay, slots int) (*Runner, context.CancelFunc) {
	t.Helper()
	r := NewRunner(RunnerOptions{Pool: env.Pool, WorkerID: id, Limits: limits, Slots: slots, Executor: exec, poll: 20 * time.Millisecond, reap: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	stop := func() { cancel(); <-done }
	t.Cleanup(stop)
	return r, stop
}

func waitFor(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTwoRunnersExecuteEachJobOnceWithinProjectConcurrency(t *testing.T) {
	env := fixture.New(t, "alice")
	var all []uuid.UUID
	for i := range 3 {
		p := env.Project(t, "alice", "P"+string(rune('0'+i)))
		all = append(all, queueJobs(t, env, p, 5)...)
	}
	rec := newRecorder(80 * time.Millisecond)
	limits := testLimits()
	startRunner(t, env, "worker-a", rec, limits, 8)
	startRunner(t, env, "worker-b", rec, limits, 8)
	waitFor(t, 15*time.Second, func() bool {
		return env.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'done'`) == len(all)
	})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, id := range all {
		if rec.runs[id] != 1 {
			t.Errorf("job %s ran %d times", id, rec.runs[id])
		}
	}
	for p, peak := range rec.peak {
		if peak > limits.ProjectConcurrency {
			t.Errorf("project %s had %d running at once", p, peak)
		}
	}
	if env.Count(t, `SELECT count(*) FROM app.jobs WHERE lease_owner IS NOT NULL`) != 0 {
		t.Error("finished jobs still hold leases")
	}
}

func TestPerProjectConcurrencyIsConfigurable(t *testing.T) {
	env := fixture.New(t, "alice")
	p := env.Project(t, "alice", "P")
	queueJobs(t, env, p, 5)
	rec := newRecorder(60 * time.Millisecond)
	limits := testLimits()
	limits.ProjectConcurrency = 1
	startRunner(t, env, "w", rec, limits, 8)
	waitFor(t, 10*time.Second, func() bool { return env.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'done'`) == 5 })
	if rec.peak[p] != 1 {
		t.Fatalf("peak %d with concurrency 1", rec.peak[p])
	}
}

// blocker never finishes its jobs (simulating a stuck worker).
type blocker struct{ started chan uuid.UUID }

func (b blocker) Execute(ctx context.Context, job Job) {
	b.started <- job.ID
	<-ctx.Done()
}

func TestLeaseExtensionAndLoss(t *testing.T) {
	env := fixture.New(t, "alice")
	p := env.Project(t, "alice", "P")
	ids := queueJobs(t, env, p, 1)
	b := blocker{started: make(chan uuid.UUID, 1)}
	r, stop := startRunner(t, env, "w", b, testLimits(), 1)
	<-b.started
	job := Job{ID: ids[0], owner: "w", runner: r}
	if err := job.Extend(context.Background()); err != nil {
		t.Fatalf("extend as holder: %v", err)
	}
	other := Job{ID: ids[0], owner: "someone-else", runner: r}
	if err := other.Extend(context.Background()); err != ErrLeaseLost {
		t.Fatalf("extend as non-holder: %v", err)
	}
	stop()
}
