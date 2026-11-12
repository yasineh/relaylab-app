-- Replay queries (add-event-replay). API reads and writes are owner-scoped
-- through app.projects; worker queries are keyed by job and replay ids and
-- every result write is checked against the job lease.

-- name: GetEventForReplay :one
-- The stored request to replay, for the project's owner.
SELECT e.id, e.method, e.headers, e.content_type, e.body
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- name: GetSinkEndpoint :one
-- A sink must be an endpoint of the same project, owned by the caller.
SELECT en.id, en.name, en.enabled
FROM app.endpoints en
JOIN app.projects p ON p.id = en.project_id
WHERE en.id = sqlc.arg(id) AND en.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- name: CountActiveReplays :one
SELECT count(*)::int FROM app.replay_attempts
WHERE project_id = sqlc.arg(project_id) AND state IN ('queued', 'running');

-- name: GetReplayByIdempotencyKey :one
SELECT id, request_fingerprint, created_at
FROM app.replay_attempts
WHERE project_id = sqlc.arg(project_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: ExpireIdempotencyKey :exec
-- Keys are honoured for 24 hours; an older row gives its key up for reuse.
UPDATE app.replay_attempts SET idempotency_key = NULL
WHERE project_id = sqlc.arg(project_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: InsertReplayAttempt :exec
INSERT INTO app.replay_attempts (
  id, project_id, event_id, actor_user_id, destination_type, target_url, sink_endpoint_id, method,
  header_override_names, header_overrides_ciphertext, idempotency_key, request_fingerprint
) VALUES (
  sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(event_id), sqlc.arg(actor_user_id), sqlc.arg(destination_type),
  sqlc.narg(target_url), sqlc.narg(sink_endpoint_id), sqlc.arg(method),
  sqlc.arg(header_override_names), sqlc.narg(header_overrides_ciphertext), sqlc.narg(idempotency_key),
  sqlc.narg(request_fingerprint)
);

-- name: InsertJob :exec
INSERT INTO app.jobs (id, kind, project_id, payload, max_attempts)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(project_id), sqlc.arg(payload), sqlc.arg(max_attempts));

-- name: NotifyJobs :exec
-- Wakes idle workers; they also poll, so a lost notification only delays.
SELECT pg_notify('replay_jobs', '');

-- name: GetReplay :one
SELECT r.id, r.project_id, r.event_id, r.actor_user_id, r.destination_type, r.target_url, r.sink_endpoint_id,
       en.name AS sink_endpoint_name, r.method, r.header_override_names, r.state, r.outcome, r.failure_message,
       r.tries, r.response_status, r.response_headers, r.response_body, r.response_truncated,
       r.started_at, r.finished_at, r.duration_ms, r.created_at
FROM app.replay_attempts r
JOIN app.projects p ON p.id = r.project_id
LEFT JOIN app.endpoints en ON en.id = r.sink_endpoint_id
WHERE r.id = sqlc.arg(id) AND r.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- name: ListReplays :many
-- Newest first, keyed by (created_at, id). The optional filters narrow
-- inside the owner's project; cursor_created_at is NULL on the first page.
SELECT r.id, r.event_id, r.destination_type, r.target_url, r.sink_endpoint_id, en.name AS sink_endpoint_name,
       r.method, r.state, r.outcome, r.tries, r.response_status, r.duration_ms, r.created_at, r.finished_at
FROM app.replay_attempts r
JOIN app.projects p ON p.id = r.project_id
LEFT JOIN app.endpoints en ON en.id = r.sink_endpoint_id
WHERE r.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND (sqlc.narg(event_id)::uuid IS NULL OR r.event_id = sqlc.narg(event_id)::uuid)
  AND (sqlc.narg(state)::text IS NULL OR r.state = sqlc.narg(state)::text)
  AND (sqlc.narg(outcome)::text IS NULL OR r.outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL
       OR (r.created_at, r.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListAuditLog :many
SELECT a.id, a.actor_user_id, u.name AS actor_name, a.action, a.resource_type, a.resource_id, a.metadata, a.created_at
FROM app.audit_log a
JOIN app.projects p ON p.id = a.project_id
LEFT JOIN auth."user" u ON u.id = a.actor_user_id
WHERE a.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL
       OR (a.created_at, a.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- Worker: job queue.

-- name: LockQueuedJobs :many
-- Candidate jobs for one claim transaction; rows locked by other workers are
-- skipped, so no two workers can claim the same job.
SELECT id, project_id FROM app.jobs
WHERE state = 'queued' AND run_at <= now() AND kind = sqlc.arg(kind)
ORDER BY run_at, id
LIMIT sqlc.arg(row_limit)
FOR UPDATE SKIP LOCKED;

-- name: LockProjectJobs :exec
-- Serializes claims for one project within the claim transaction, so the
-- running count below is exact.
SELECT pg_advisory_xact_lock(727100, hashtext(sqlc.arg(project_id)::uuid::text));

-- name: CountRunningJobs :one
SELECT count(*)::int FROM app.jobs WHERE project_id = sqlc.arg(project_id) AND state = 'running';

-- name: MarkJobRunning :one
UPDATE app.jobs
SET state = 'running', lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int),
    attempts = attempts + 1, updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'queued'
RETURNING id, project_id, payload, attempts, max_attempts;

-- name: ExtendJobLease :execrows
UPDATE app.jobs SET lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int), updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_owner = sqlc.arg(lease_owner);

-- name: FinishJob :execrows
-- Only the lease holder may finish a job.
UPDATE app.jobs SET state = sqlc.arg(state), lease_owner = NULL, lease_expires_at = NULL,
    last_error = sqlc.narg(last_error), updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'running' AND lease_owner = sqlc.arg(lease_owner);

-- name: ReapExpiredJobs :many
-- Expired leases go back to the queue, or to dead after max_attempts claims.
UPDATE app.jobs
SET state = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'queued' END,
    lease_owner = NULL, lease_expires_at = NULL, run_at = now(), last_error = 'lease_expired', updated_at = now()
WHERE state = 'running' AND lease_expires_at < now()
RETURNING id, kind, state, payload;

-- name: CountQueuedJobs :one
SELECT count(*)::int FROM app.jobs WHERE kind = sqlc.arg(kind) AND state = 'queued';

-- Worker: replay delivery.

-- name: GetReplayForDelivery :one
-- The replay and its event, for the job holding the lease.
SELECT r.id, r.project_id, r.destination_type, r.target_url, r.sink_endpoint_id, r.method,
       r.header_override_names, r.header_overrides_ciphertext, r.state,
       e.id AS event_id, e.headers AS event_headers, e.content_type AS event_content_type, e.body AS event_body
FROM app.replay_attempts r
JOIN app.webhook_events e ON e.id = r.event_id
WHERE r.id = sqlc.arg(id);

-- name: MarkReplayRunning :execrows
UPDATE app.replay_attempts r
SET state = 'running', started_at = COALESCE(r.started_at, now()), updated_at = now()
WHERE r.id = sqlc.arg(id) AND r.state IN ('queued', 'running')
  AND EXISTS (SELECT 1 FROM app.jobs j WHERE j.id = sqlc.arg(job_id) AND j.lease_owner = sqlc.arg(lease_owner) AND j.state = 'running');

-- name: CompleteReplay :execrows
-- Writes the result only while the caller still holds the job's lease, and
-- erases the override ciphertext.
UPDATE app.replay_attempts r
SET state = sqlc.arg(state), outcome = sqlc.arg(outcome), failure_message = sqlc.narg(failure_message),
    tries = sqlc.arg(tries), response_status = sqlc.narg(response_status),
    response_headers = sqlc.narg(response_headers), response_body = sqlc.narg(response_body),
    response_truncated = sqlc.arg(response_truncated), finished_at = now(),
    duration_ms = sqlc.arg(duration_ms), header_overrides_ciphertext = NULL, updated_at = now()
WHERE r.id = sqlc.arg(id) AND r.state IN ('queued', 'running')
  AND EXISTS (SELECT 1 FROM app.jobs j WHERE j.id = sqlc.arg(job_id) AND j.lease_owner = sqlc.arg(lease_owner) AND j.state = 'running');

-- name: FailReplayWorkerLost :execrows
-- A dead job's replay ends failed/worker_lost, with secrets erased.
UPDATE app.replay_attempts
SET state = 'failed', outcome = 'worker_lost', failure_message = sqlc.arg(failure_message),
    finished_at = now(), header_overrides_ciphertext = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND state IN ('queued', 'running');

-- name: GetSinkForDelivery :one
SELECT id, project_id, enabled FROM app.endpoints WHERE id = sqlc.arg(id);

-- Worker heartbeats.

-- name: UpsertWorkerHeartbeat :exec
INSERT INTO app.worker_heartbeats (worker_id, outbound_guard, version, seen_at)
VALUES (sqlc.arg(worker_id), sqlc.arg(outbound_guard), sqlc.arg(version), now())
ON CONFLICT (worker_id) DO UPDATE SET outbound_guard = EXCLUDED.outbound_guard, version = EXCLUDED.version, seen_at = now();

-- name: GuardActiveRecently :one
-- URL replay is available while some worker reported an active guard within
-- the window.
SELECT EXISTS (
  SELECT 1 FROM app.worker_heartbeats
  WHERE outbound_guard = 'active' AND seen_at > now() - make_interval(secs => sqlc.arg(window_seconds)::int)
)::boolean;

-- name: DeleteStaleWorkerHeartbeats :exec
DELETE FROM app.worker_heartbeats WHERE seen_at < now() - interval '1 day';
