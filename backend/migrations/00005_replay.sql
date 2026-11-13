-- Replay (add-event-replay): the job queue, replay attempts, and worker
-- heartbeats. Replay creation writes to the existing app.audit_log.
--
-- Deletion contract (D18): deleting a project removes its jobs and replay
-- attempts in the same transaction (ON DELETE CASCADE), including queued and
-- running jobs. Deleting an event removes its replay attempts. Deleting a sink
-- endpoint keeps the replay and clears only sink_endpoint_id. Deleting a user
-- keeps their replays and clears actor_user_id.

-- +goose Up
-- Targets for the composite foreign keys below (id alone is already unique).
ALTER TABLE app.webhook_events ADD CONSTRAINT webhook_events_id_project_key UNIQUE (id, project_id);

-- Postgres-backed job queue (D5). Workers claim with FOR UPDATE SKIP LOCKED
-- and hold a lease; attempts counts claims, so a job whose lease expires
-- max_attempts times becomes dead.
CREATE TABLE app.jobs (
  id               uuid PRIMARY KEY, -- UUIDv7
  kind             text NOT NULL CHECK (char_length(kind) BETWEEN 1 AND 64),
  project_id       uuid REFERENCES app.projects (id) ON DELETE CASCADE,
  payload          jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
  state            text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done', 'dead')),
  run_at           timestamptz NOT NULL DEFAULT now(),
  attempts         integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts     integer NOT NULL CHECK (max_attempts > 0),
  lease_owner      text CHECK (char_length(lease_owner) BETWEEN 1 AND 128),
  lease_expires_at timestamptz,
  last_error       text CHECK (char_length(last_error) <= 200), -- a category, never raw errors
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT jobs_lease_consistent CHECK ((state = 'running') = (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL))
);

CREATE INDEX jobs_queued_run_at_idx ON app.jobs (run_at) WHERE state = 'queued';
CREATE INDEX jobs_project_state_idx ON app.jobs (project_id, state);
CREATE INDEX jobs_running_lease_idx ON app.jobs (lease_expires_at) WHERE state = 'running';

COMMENT ON TABLE app.jobs IS 'Background job queue (replay.deliver); claimed with SKIP LOCKED leases';

-- One row per user-initiated replay; tries counts deliveries within it.
CREATE TABLE app.replay_attempts (
  id                          uuid PRIMARY KEY, -- UUIDv7
  project_id                  uuid NOT NULL REFERENCES app.projects (id) ON DELETE CASCADE,
  event_id                    uuid NOT NULL,
  actor_user_id               text REFERENCES auth."user" (id) ON DELETE SET NULL,
  destination_type            text NOT NULL CHECK (destination_type IN ('url', 'sink')),
  target_url                  text CHECK (char_length(target_url) BETWEEN 1 AND 2048),
  sink_endpoint_id            uuid,
  method                      text NOT NULL CHECK (method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE')),
  header_override_names       text[] NOT NULL DEFAULT '{}',
  -- AES-256-GCM of the override values; null once the replay is terminal.
  header_overrides_ciphertext bytea,
  state                       text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
  -- 'succeeded' or the failure category, set when terminal.
  outcome                     text CHECK (char_length(outcome) BETWEEN 1 AND 64),
  failure_message             text CHECK (char_length(failure_message) <= 300),
  tries                       integer NOT NULL DEFAULT 0 CHECK (tries >= 0),
  response_status             integer CHECK (response_status BETWEEN 100 AND 999),
  response_headers            jsonb CHECK (jsonb_typeof(response_headers) = 'array'),
  response_body               bytea,
  response_truncated          boolean NOT NULL DEFAULT false,
  started_at                  timestamptz,
  finished_at                 timestamptz,
  duration_ms                 integer CHECK (duration_ms >= 0),
  idempotency_key             text CHECK (char_length(idempotency_key) BETWEEN 1 AND 128),
  request_fingerprint         bytea,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT replay_attempts_event_fk FOREIGN KEY (event_id, project_id)
    REFERENCES app.webhook_events (id, project_id) ON DELETE CASCADE,
  -- A sink is always an endpoint of the same project. Deleting it clears only
  -- sink_endpoint_id (column-list SET NULL keeps project_id).
  CONSTRAINT replay_attempts_sink_fk FOREIGN KEY (sink_endpoint_id, project_id)
    REFERENCES app.endpoints (id, project_id) ON DELETE SET NULL (sink_endpoint_id),
  CONSTRAINT replay_attempts_destination CHECK (
    (destination_type = 'url' AND target_url IS NOT NULL AND sink_endpoint_id IS NULL)
    OR (destination_type = 'sink' AND target_url IS NULL)
  ),
  CONSTRAINT replay_attempts_terminal CHECK ((state IN ('succeeded', 'failed')) = (outcome IS NOT NULL)),
  CONSTRAINT replay_attempts_secrets_erased CHECK (state NOT IN ('succeeded', 'failed') OR header_overrides_ciphertext IS NULL)
);

CREATE UNIQUE INDEX replay_attempts_idempotency_idx ON app.replay_attempts (project_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX replay_attempts_project_created_idx ON app.replay_attempts (project_id, created_at DESC, id DESC);
CREATE INDEX replay_attempts_event_created_idx ON app.replay_attempts (event_id, created_at DESC, id DESC);
CREATE INDEX replay_attempts_active_idx ON app.replay_attempts (project_id, state) WHERE state IN ('queued', 'running');
CREATE INDEX replay_attempts_sink_idx ON app.replay_attempts (sink_endpoint_id) WHERE sink_endpoint_id IS NOT NULL;

COMMENT ON TABLE app.replay_attempts IS 'Replays of stored events; override values only encrypted and erased when terminal';

-- Events created by a replay to a test sink are marked with their origin.
-- The reference is cleared when the replay is deleted.
ALTER TABLE app.webhook_events
  ADD COLUMN source text NOT NULL DEFAULT 'ingest' CHECK (source IN ('ingest', 'replay')),
  ADD COLUMN replay_attempt_id uuid REFERENCES app.replay_attempts (id) ON DELETE SET NULL,
  ADD CONSTRAINT webhook_events_source_consistent CHECK (source = 'replay' OR replay_attempt_id IS NULL);

-- Workers report the outbound guard's self-check here; the API offers URL
-- replay only while a recent heartbeat says active.
CREATE TABLE app.worker_heartbeats (
  worker_id      text PRIMARY KEY CHECK (char_length(worker_id) BETWEEN 1 AND 128),
  outbound_guard text NOT NULL CHECK (outbound_guard IN ('active', 'inactive')),
  version        text NOT NULL CHECK (char_length(version) BETWEEN 1 AND 64),
  seen_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app.worker_heartbeats;
ALTER TABLE app.webhook_events DROP CONSTRAINT webhook_events_source_consistent,
  DROP COLUMN replay_attempt_id, DROP COLUMN source;
DROP TABLE app.replay_attempts;
DROP TABLE app.jobs;
ALTER TABLE app.webhook_events DROP CONSTRAINT webhook_events_id_project_key;
