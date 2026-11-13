package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/ingestion"
	"relaylab/internal/platform/config"
	"relaylab/internal/platform/db/store"
	"relaylab/internal/platform/redact"
	"relaylab/internal/replay/outbound"
)

// Replay-specific failure categories (besides the guard's).
const (
	CategorySinkDisabled   = "sink_disabled"
	CategoryWorkerLost     = "worker_lost"
	CategoryOverrideFailed = "override_unavailable"
	CategoryURLDisabled    = "url_replay_disabled"
)

const (
	urlDisabledMessage    = "Replay to URLs was turned off on this server before this replay was sent. Nothing was sent."
	workerLostMessage     = "The worker stopped repeatedly while delivering this replay. It may have been delivered more than once."
	sinkDisabledMessage   = "The test sink endpoint is disabled or was deleted."
	overrideFailedMessage = "The added headers could not be read, so nothing was sent."
)

// sinkResponse is the synthetic response recorded for sink deliveries.
var sinkResponse = []byte(`{"received":true}`)

// Sender sends one guarded request (outbound.Client).
type Sender interface {
	Do(ctx context.Context, req outbound.Request) outbound.Result
	Active() bool
}

// Deliverer executes replay jobs: it loads the replay, sends it (to a URL
// through the outbound guard, or to a test sink internally), and records
// the result, all under the job's lease.
type Deliverer struct {
	pool    *pgxpool.Pool
	runner  *Runner
	sender  Sender
	sealer  *Sealer
	red     *redact.Redactor
	sink    ingestion.Store
	limits  *config.Replay
	logger  *slog.Logger
	metrics *Metrics
	backoff []time.Duration // delay before try 2, 3, ...
}

// DelivererOptions configure a Deliverer.
type DelivererOptions struct {
	Pool    *pgxpool.Pool
	Sender  Sender
	Sealer  *Sealer
	Red     *redact.Redactor
	Limits  *config.Replay
	Logger  *slog.Logger
	Metrics *Metrics
	Backoff []time.Duration // default 2 s, 8 s (spec)
}

// NewDeliverer returns the replay executor; attach it to a runner with
// Bind before running.
func NewDeliverer(o DelivererOptions) *Deliverer {
	d := &Deliverer{
		pool: o.Pool, sender: o.Sender, sealer: o.Sealer, red: o.Red, sink: ingestion.NewPGStore(o.Pool),
		limits: o.Limits, logger: o.Logger, metrics: o.Metrics, backoff: o.Backoff,
	}
	if d.logger == nil {
		d.logger = slog.New(slog.DiscardHandler)
	}
	if d.backoff == nil {
		d.backoff = []time.Duration{2 * time.Second, 8 * time.Second}
	}
	return d
}

// Bind connects the deliverer to its runner (for lease-checked finishing).
func (d *Deliverer) Bind(r *Runner) { d.runner = r }

// outcome is one replay's result.
type outcome struct {
	state, category, message string
	tries                    int
	status                   *int
	headers                  [][2]string
	body                     []byte
	truncated                bool
}

// Execute implements Executor.
func (d *Deliverer) Execute(ctx context.Context, job Job) {
	start := time.Now()
	log := d.logger.With(slog.String("job_id", job.ID.String()), slog.String("replay_id", job.ReplayID.String()),
		slog.String("project_id", job.ProjectID.String()))
	q := store.New(d.pool)
	row, err := q.GetReplayForDelivery(ctx, pgUUID(job.ReplayID))
	if errors.Is(err, pgx.ErrNoRows) {
		// Deleted with its project or event: nothing to send or record.
		log.Info("replay_missing")
		_, _ = d.runner.Finish(ctx, q, job, "done", "replay_missing")
		return
	}
	if err != nil {
		log.Error("load replay failed", slog.String("error", err.Error()))
		return // the lease expires and the job is retried
	}
	if n, err := q.MarkReplayRunning(ctx, store.MarkReplayRunningParams{ID: row.ID, JobID: pgUUID(job.ID), LeaseOwner: pgText(job.owner)}); err != nil || n == 0 {
		log.Info("replay_missing", slog.String("reason", "not runnable or lease lost"))
		return
	}

	host := ""
	var out outcome
	switch row.DestinationType {
	case DestinationSink:
		out = d.deliverSink(ctx, row)
	default:
		host = hostOf(row.TargetUrl.String)
		if !d.limits.Enabled {
			// Queued while URL replay was on; it has been turned off since.
			out = outcome{state: "failed", category: CategoryURLDisabled, message: urlDisabledMessage}
			break
		}
		out = d.deliverURL(ctx, job, row)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return // shutting down: the lease expires and another worker retries
	}
	if out.category == "lease_lost" {
		log.Info("replay_missing", slog.String("reason", "lease lost during delivery"))
		return
	}
	elapsed := time.Since(start)
	written, err := d.complete(ctx, job, row.ID, out, elapsed)
	switch {
	case err != nil:
		log.Error("record replay result failed", slog.String("error", err.Error()))
		return
	case !written:
		log.Info("replay_missing", slog.String("reason", "rows changed during delivery"))
		return
	}
	d.metrics.replayDone(out.state, out.category, elapsed)
	// Never bodies, header values, or query strings.
	attrs := []any{
		slog.String("outcome", out.category), slog.Int("tries", out.tries),
		slog.Int64("duration_ms", elapsed.Milliseconds()), slog.String("destination_type", row.DestinationType),
	}
	if host != "" {
		attrs = append(attrs, slog.String("destination_host", host))
	}
	if out.status != nil {
		attrs = append(attrs, slog.Int("response_status", *out.status))
	}
	log.Info("replay finished", attrs...)
}

// build reconstructs the request from the stored event and the decrypted
// overrides.
func (d *Deliverer) build(row store.GetReplayForDeliveryRow) (Built, bool) {
	var headers [][2]string
	_ = json.Unmarshal(row.EventHeaders, &headers)
	var overrides []Override
	if len(row.HeaderOverridesCiphertext) > 0 {
		var err error
		if d.sealer == nil {
			return Built{}, false
		}
		if overrides, err = d.sealer.Open(row.HeaderOverridesCiphertext, row.ID.Bytes); err != nil {
			return Built{}, false
		}
	}
	return BuildRequest(StoredEvent{Headers: headers, Body: row.EventBody}, row.Method, overrides, row.ID.Bytes, d.red), true
}

// retryable reports whether a try may be repeated (spec: bounded retries).
func retryable(r outbound.Result) bool {
	switch r.Category {
	case outbound.ConnectFailure, outbound.DNSFailure, outbound.Timeout:
		return true
	case outbound.Succeeded:
		return r.Status == http.StatusBadGateway || r.Status == http.StatusServiceUnavailable || r.Status == http.StatusGatewayTimeout
	}
	return false
}

func (d *Deliverer) deliverURL(ctx context.Context, job Job, row store.GetReplayForDeliveryRow) outcome {
	b, ok := d.build(row)
	if !ok {
		return outcome{state: "failed", category: CategoryOverrideFailed, message: overrideFailedMessage}
	}
	if !d.sender.Active() {
		return outcome{state: "failed", category: string(outbound.GuardInactive), message: outbound.MessageFor(outbound.GuardInactive)}
	}
	req := outbound.Request{Method: b.Method, URL: row.TargetUrl.String, Header: b.Header, Explicit: b.Explicit, Body: b.Body}
	var res outbound.Result
	tries := 0
	for tries < d.limits.MaxTries {
		if tries > 0 {
			wait := d.backoff[min(tries-1, len(d.backoff)-1)]
			wait += time.Duration(rand.Int64N(int64(wait)/4 + 1)) //nolint:gosec // jitter, not security
			sleep(ctx, wait)
			if ctx.Err() != nil {
				return outcome{}
			}
		}
		if err := job.Extend(ctx); err != nil {
			return outcome{category: "lease_lost"}
		}
		tries++
		res = d.sender.Do(ctx, req)
		if !retryable(res) {
			break
		}
	}
	if res.Category != outbound.Succeeded {
		return outcome{state: "failed", category: string(res.Category), message: res.Message, tries: tries}
	}
	status := res.Status
	body := res.Body
	truncated := res.Truncated
	if len(body) > d.limits.ResponseExcerptBytes {
		body, truncated = body[:d.limits.ResponseExcerptBytes], true
	}
	return outcome{
		state: "succeeded", category: "succeeded", tries: tries, status: &status,
		headers: d.responseHeaders(res.Header), body: body, truncated: truncated,
	}
}

// responseHeaders lists response headers with sensitive values replaced,
// capped at maxResponseHeaderJSON bytes of JSON.
func (d *Deliverer) responseHeaders(h http.Header) [][2]string {
	var list [][2]string
	for name, values := range h {
		for _, v := range values {
			list = append(list, [2]string{name, v})
		}
	}
	sortPairs(list)
	list, _ = d.red.Headers(list)
	size := 2
	for i, p := range list {
		size += len(p[0]) + len(p[1]) + 8
		if size > maxResponseHeaderJSON() {
			return list[:i]
		}
	}
	return list
}

func (d *Deliverer) deliverSink(ctx context.Context, row store.GetReplayForDeliveryRow) outcome {
	if !row.SinkEndpointID.Valid {
		return outcome{state: "failed", category: CategorySinkDisabled, message: sinkDisabledMessage, tries: 1}
	}
	b, ok := d.build(row)
	if !ok {
		return outcome{state: "failed", category: CategoryOverrideFailed, message: overrideFailedMessage}
	}
	var headers [][2]string
	for name, values := range b.Header {
		for _, v := range values {
			headers = append(headers, [2]string{name, v})
		}
	}
	for name, values := range b.Explicit {
		for _, v := range values {
			headers = append(headers, [2]string{name, v})
		}
	}
	_, err := ingestion.StoreInternal(ctx, d.sink, store.New(d.pool), d.red, row.SinkEndpointID.Bytes, ingestion.InternalRequest{
		Method: b.Method, Headers: headers, Body: b.Body, ReplayID: row.ID.Bytes,
	})
	switch {
	case errors.Is(err, ingestion.ErrSinkDisabled), errors.Is(err, ingestion.ErrUnknownEndpoint):
		return outcome{state: "failed", category: CategorySinkDisabled, message: sinkDisabledMessage, tries: 1}
	case err != nil:
		return outcome{state: "failed", category: string(outbound.ConnectFailure), message: "The test sink could not store the event.", tries: 1}
	}
	status := http.StatusOK
	return outcome{
		state: "succeeded", category: "succeeded", tries: 1, status: &status,
		headers: [][2]string{{"Content-Type", "application/json"}}, body: sinkResponse,
	}
}

// complete records the result and finishes the job in one transaction, only
// while this worker holds the lease. It reports false when the replay or job
// changed or vanished; nothing is written then.
func (d *Deliverer) complete(ctx context.Context, job Job, replayID pgtype.UUID, out outcome, elapsed time.Duration) (bool, error) {
	written := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		arg := store.CompleteReplayParams{
			ID: replayID, JobID: pgUUID(job.ID), LeaseOwner: pgText(job.owner), State: out.state,
			Outcome: pgText(out.category), FailureMessage: optText(out.message), Tries: int32(out.tries), //nolint:gosec // at most REPLAY_MAX_TRIES
			ResponseTruncated: out.truncated, DurationMs: pgtype.Int4{Int32: int32(min(elapsed.Milliseconds(), 1<<31-1)), Valid: true}, //nolint:gosec // clamped
		}
		if out.status != nil {
			arg.ResponseStatus = pgtype.Int4{Int32: int32(*out.status), Valid: true} //nolint:gosec // HTTP status
			hj, err := json.Marshal(out.headers)
			if err != nil {
				return err
			}
			if out.headers == nil {
				hj = []byte("[]")
			}
			arg.ResponseHeaders = hj
			arg.ResponseBody = out.body
			if arg.ResponseBody == nil {
				arg.ResponseBody = []byte{}
			}
		}
		n, err := q.CompleteReplay(ctx, arg)
		if err != nil {
			return fmt.Errorf("complete replay: %w", err)
		}
		if n == 0 {
			return errNothingWritten
		}
		ok, err := d.runner.Finish(ctx, q, job, "done", "")
		if err != nil {
			return err
		}
		if !ok {
			return errNothingWritten
		}
		written = true
		return nil
	})
	if errors.Is(err, errNothingWritten) {
		// The lease may still be ours if only the replay row vanished.
		_, _ = d.runner.Finish(ctx, store.New(d.pool), job, "done", "replay_missing")
		return false, nil
	}
	return written, err
}

var errNothingWritten = errors.New("nothing written")

func pgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func optText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func sortPairs(list [][2]string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j][0] < list[j-1][0]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// maxResponseHeaderJSON caps stored response headers at the guard's response
// header limit (spec: 32 KiB).
func maxResponseHeaderJSON() int { return outbound.MaxResponseHeaderBytes }
