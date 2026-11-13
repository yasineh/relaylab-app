-- Indexes for the retention purge (add-event-retention).
--
-- The worker deletes, in batches of at most 1,000 rows:
--   * events older than RETENTION_EVENT_TTL, oldest first;
--   * per project, the oldest events beyond RETENTION_MAX_EVENTS_PER_PROJECT
--     (ordered by received_at, then id, so ties are deterministic);
--   * audit entries older than RETENTION_AUDIT_TTL;
--   * done or dead jobs older than RETENTION_JOB_TTL.
-- Deleting events cascades to their replay attempts, and deleting a replay
-- attempt clears webhook_events.replay_attempt_id on the sink events it
-- created; that clearing needs its own index to stay cheap.

-- +goose Up
CREATE INDEX webhook_events_received_idx ON app.webhook_events (received_at);
-- Replaces (project_id, received_at): the same prefix plus the id tie-breaker.
CREATE INDEX webhook_events_project_received_id_idx ON app.webhook_events (project_id, received_at, id);
DROP INDEX app.webhook_events_project_received_idx;
CREATE INDEX webhook_events_replay_attempt_idx ON app.webhook_events (replay_attempt_id)
  WHERE replay_attempt_id IS NOT NULL;
CREATE INDEX audit_log_created_idx ON app.audit_log (created_at);
CREATE INDEX jobs_finished_updated_idx ON app.jobs (updated_at) WHERE state IN ('done', 'dead');

-- +goose Down
DROP INDEX app.jobs_finished_updated_idx;
DROP INDEX app.audit_log_created_idx;
DROP INDEX app.webhook_events_replay_attempt_idx;
CREATE INDEX webhook_events_project_received_idx ON app.webhook_events (project_id, received_at);
DROP INDEX app.webhook_events_project_received_id_idx;
DROP INDEX app.webhook_events_received_idx;
