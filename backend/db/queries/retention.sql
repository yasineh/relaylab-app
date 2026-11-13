-- Retention purge (add-event-retention). Every statement deletes at most
-- row_limit rows; the worker loops until a batch comes back short. Ages are
-- measured with the database clock (now()), never the worker's.

-- name: PurgeExpiredEvents :one
-- Oldest events received before now() - ttl. Their replay attempts go by
-- ON DELETE CASCADE; replays is how many were removed with them.
WITH doomed AS (
  SELECT id FROM app.webhook_events
  WHERE received_at < now() - make_interval(secs => sqlc.arg(ttl_seconds)::float8)
  ORDER BY received_at
  LIMIT sqlc.arg(row_limit)
), replays AS (
  SELECT count(*) AS n FROM app.replay_attempts WHERE event_id IN (SELECT id FROM doomed)
), gone AS (
  DELETE FROM app.webhook_events WHERE id IN (SELECT id FROM doomed) RETURNING 1
)
SELECT (SELECT count(*) FROM gone)::bigint AS events, (SELECT n FROM replays)::bigint AS replays;

-- name: ProjectsOverEventCap :many
-- Projects holding more than cap events. Fine at MVP scale; a maintained
-- counter is the follow-up if this scan grows expensive.
SELECT project_id FROM app.webhook_events
GROUP BY project_id
HAVING count(*) > sqlc.arg(cap)::bigint;

-- name: EventCapBoundary :one
-- The newest event that falls outside the cap: everything at or before it
-- (by received_at, then id) is beyond the cap.
SELECT received_at, id FROM app.webhook_events
WHERE project_id = sqlc.arg(project_id)
ORDER BY received_at DESC, id DESC
OFFSET sqlc.arg(cap)::bigint
LIMIT 1;

-- name: PurgeProjectEventsThrough :one
-- One batch of a project's events at or before the cap boundary, oldest first.
WITH doomed AS (
  SELECT e.id FROM app.webhook_events e
  WHERE e.project_id = sqlc.arg(project_id)::uuid
    AND (e.received_at, e.id) <= (sqlc.arg(received_at)::timestamptz, sqlc.arg(id)::uuid)
  ORDER BY e.received_at, e.id
  LIMIT sqlc.arg(row_limit)
), replays AS (
  SELECT count(*) AS n FROM app.replay_attempts WHERE event_id IN (SELECT id FROM doomed)
), gone AS (
  DELETE FROM app.webhook_events WHERE id IN (SELECT id FROM doomed) RETURNING 1
)
SELECT (SELECT count(*) FROM gone)::bigint AS events, (SELECT n FROM replays)::bigint AS replays;

-- name: PurgeAuditLog :execrows
DELETE FROM app.audit_log WHERE id IN (
  SELECT id FROM app.audit_log
  WHERE created_at < now() - make_interval(secs => sqlc.arg(ttl_seconds)::float8)
  ORDER BY created_at
  LIMIT sqlc.arg(row_limit)
);

-- name: PurgeFinishedJobs :execrows
-- Only done and dead jobs; queued and running jobs are never purged by age.
DELETE FROM app.jobs WHERE id IN (
  SELECT id FROM app.jobs
  WHERE state IN ('done', 'dead')
    AND updated_at < now() - make_interval(secs => sqlc.arg(ttl_seconds)::float8)
  ORDER BY updated_at
  LIMIT sqlc.arg(row_limit)
);

-- name: PurgeOrphanedJobs :execrows
-- Safety net (D18): project deletion already removes its jobs in the same
-- transaction, so this normally finds nothing. Jobs whose project, or whose
-- replay attempt, no longer exists are removed, except running jobs: their
-- worker finishes them, and its lease-checked writes then affect no rows.
DELETE FROM app.jobs WHERE id IN (
  SELECT j.id FROM app.jobs j
  WHERE j.state <> 'running'
    AND (
      (j.project_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM app.projects p WHERE p.id = j.project_id))
      OR (j.kind = 'replay.deliver' AND NOT EXISTS (
        SELECT 1 FROM app.replay_attempts r
        WHERE r.id = CASE WHEN j.payload->>'replay_id' ~ '^[0-9a-fA-F-]{36}$'
                          THEN (j.payload->>'replay_id')::uuid END))
    )
  LIMIT sqlc.arg(row_limit)
);

-- name: TryRetentionLock :one
-- Session-level lock held for one purge run, so only one worker purges.
SELECT pg_try_advisory_lock(727101) AS acquired;

-- name: ReleaseRetentionLock :one
SELECT pg_advisory_unlock(727101) AS released;

-- name: DemoProjectsOverEventCap :many
-- Demo users' projects holding more than the demo cap (DEMO_MAX_EVENTS).
SELECT e.project_id FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
JOIN auth."user" u ON u.id = p.owner_id
WHERE u."isAnonymous" IS TRUE
GROUP BY e.project_id
HAVING count(*) > sqlc.arg(cap)::bigint;
