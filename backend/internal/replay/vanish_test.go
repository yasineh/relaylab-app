package replay

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"relaylab/internal/platform/config"
	"relaylab/internal/retention"
)

// syncBuffer is a log sink safe for concurrent writers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestProjectDeletedDuringDelivery(t *testing.T) {
	e := newExecEnv(t)
	e.opts.ResponseHeaderTimeout = 5 * time.Second
	e.opts.TotalTimeout = 10 * time.Second
	var hits atomic.Int32
	inFlight := make(chan struct{})
	release := make(chan struct{})
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			close(inFlight)
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})
	e.build()
	logs := &syncBuffer{}
	e.deliverer.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.runner.logger = e.deliverer.logger

	doomed := e.queue(url, nil, []Override{{"Authorization", "Bearer s3cret"}})
	// Another project's replay, to show the runner keeps going.
	other := e.Project(t, "alice", "Other")
	api := &apiEnv{Env: e.Env, t: t}
	otherSource := api.endpoint(t, other, "S", true)
	otherSink := api.endpoint(t, other, "Sink", true)
	otherEvent := api.storeEvent(t, other, otherSource, [][2]string{}, []byte("x"), "text/plain")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.runner.Run(ctx)
	<-inFlight

	// The owner deletes the project mid-delivery.
	if _, err := e.Admin.Exec(context.Background(), `DELETE FROM app.projects WHERE id = $1`, e.project); err != nil {
		t.Fatal(err)
	}
	close(release)

	// Queue work in the other project after the deletion.
	id := uuid.Must(uuid.NewV7())
	if _, err := e.Admin.Exec(context.Background(), `INSERT INTO app.replay_attempts (id, project_id, event_id, destination_type, sink_endpoint_id, method)
		VALUES ($1, $2, $3, 'sink', $4, 'POST')`, id, other, otherEvent, otherSink); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Admin.Exec(context.Background(), `INSERT INTO app.jobs (id, kind, project_id, payload, max_attempts)
		VALUES ($1, 'replay.deliver', $2, jsonb_build_object('replay_id', $3::text), 3)`, uuid.Must(uuid.NewV7()), other, id.String()); err != nil {
		t.Fatal(err)
	}
	e.runner.Wake()
	waitFor(t, 5*time.Second, func() bool {
		return e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1 AND state = 'succeeded'`, id) == 1
	})

	out := logs.String()
	if !strings.Contains(out, `"msg":"replay_missing"`) || !strings.Contains(out, doomed.String()) {
		t.Fatalf("no replay_missing log: %s", out)
	}
	if strings.Contains(out, `"level":"ERROR"`) || strings.Contains(out, `"level":"WARN"`) {
		t.Fatalf("vanished rows logged as a problem: %s", out)
	}
	if e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE project_id = $1`, e.project) != 0 ||
		e.Count(t, `SELECT count(*) FROM app.jobs WHERE project_id = $1`, e.project) != 0 {
		t.Fatal("rows written back for the deleted project")
	}
	if strings.Contains(out, "s3cret") {
		t.Fatal("override value logged")
	}
}

// TestEventDeletedDuringDelivery: the owner deletes the event (or retention
// expires it) while its replay is being delivered. The replay goes with the
// event; the retention orphan sweep leaves the running job to its worker,
// which finishes it once, without error or retry.
func TestEventDeletedDuringDelivery(t *testing.T) {
	e := newExecEnv(t)
	e.opts.ResponseHeaderTimeout = 5 * time.Second
	e.opts.TotalTimeout = 10 * time.Second
	var hits atomic.Int32
	inFlight := make(chan struct{})
	release := make(chan struct{})
	url := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			close(inFlight)
			<-release
		}
		w.WriteHeader(http.StatusInternalServerError) // would be retried if the replay still existed
	})
	e.build()
	logs := &syncBuffer{}
	e.deliverer.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.runner.logger = e.deliverer.logger

	doomed := e.queue(url, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.runner.Run(ctx)
	<-inFlight

	if _, err := e.Admin.Exec(context.Background(), `DELETE FROM app.webhook_events WHERE id = $1`, e.event); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Retention{EventTTL: time.Hour, MaxEventsPerProject: 10, AuditTTL: time.Hour, JobTTL: time.Hour, Interval: time.Minute}
	res, ran, err := retention.New(e.Pool, cfg, nil, nil).RunOnce(context.Background())
	if err != nil || !ran || res.OrphanedJobs != 0 {
		t.Fatalf("sweep: ran=%v res=%+v err=%v", ran, res, err)
	}
	if e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'running'`) != 1 {
		t.Fatal("running job swept")
	}
	close(release)

	waitFor(t, 5*time.Second, func() bool {
		return e.Count(t, `SELECT count(*) FROM app.jobs WHERE state = 'done'`) == 1
	})
	time.Sleep(200 * time.Millisecond)
	if hits.Load() != 1 {
		t.Fatalf("delivered %d times", hits.Load())
	}
	out := logs.String()
	if strings.Contains(out, `"level":"ERROR"`) || strings.Contains(out, `"level":"WARN"`) {
		t.Fatalf("vanished replay logged as a problem: %s", out)
	}
	if e.Count(t, `SELECT count(*) FROM app.replay_attempts WHERE id = $1`, doomed) != 0 {
		t.Fatal("replay written back")
	}
}
