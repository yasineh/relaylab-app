-- Received webhook requests (add-webhook-ingestion).
--
-- Each row is one accepted request, written before the sender is acknowledged.
-- Sensitive header and query values are replaced with '[REDACTED]' before the
-- insert (D9), so original credentials never reach this table. Body bytes are
-- stored unmodified. Rows are never updated after insert.
--
-- project_id is denormalized for project-scoped queries; the composite foreign
-- key guarantees it always matches the endpoint's project.
--
-- Deletion contract (D18): rows cascade from app.endpoints and app.projects.

-- +goose Up
-- Target for the composite foreign key below (id alone is already unique).
ALTER TABLE app.endpoints ADD CONSTRAINT endpoints_id_project_key UNIQUE (id, project_id);

CREATE TABLE app.webhook_events (
  id                    uuid PRIMARY KEY, -- UUIDv7: time-ordered, used as the feed cursor
  endpoint_id           uuid NOT NULL,
  project_id            uuid NOT NULL REFERENCES app.projects (id) ON DELETE CASCADE,
  received_at           timestamptz NOT NULL,
  method                text NOT NULL CHECK (method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS')),
  path                  text NOT NULL CHECK (char_length(path) <= 2048), -- subpath after the token, '' for none
  raw_query             text NOT NULL DEFAULT '' CHECK (char_length(raw_query) <= 8192),
  query_params          jsonb NOT NULL CHECK (jsonb_typeof(query_params) = 'object'), -- {name: [values]}
  headers               jsonb NOT NULL CHECK (jsonb_typeof(headers) = 'array'), -- [[name, value], ...] sorted by name
  redacted_header_count smallint NOT NULL DEFAULT 0 CHECK (redacted_header_count >= 0),
  content_type          text CHECK (char_length(content_type) <= 255),
  body                  bytea NOT NULL,
  body_size             integer NOT NULL CHECK (body_size >= 0 AND body_size = octet_length(body)),
  body_encoding         text NOT NULL CHECK (body_encoding IN ('utf8', 'binary')),
  body_json             jsonb, -- parsed copy when the body is valid JSON with a JSON content type
  json_invalid          boolean NOT NULL DEFAULT false,
  source_ip_prefix      inet, -- truncated: IPv4 /24, IPv6 /48
  user_agent            text CHECK (char_length(user_agent) <= 512),
  correlation_id        text NOT NULL CHECK (char_length(correlation_id) BETWEEN 1 AND 64),
  CONSTRAINT webhook_events_endpoint_fk FOREIGN KEY (endpoint_id, project_id)
    REFERENCES app.endpoints (id, project_id) ON DELETE CASCADE,
  CONSTRAINT webhook_events_json_consistent CHECK (NOT (json_invalid AND body_json IS NOT NULL))
);

-- Project feed and cursor pagination (newest first).
CREATE INDEX webhook_events_project_id_idx ON app.webhook_events (project_id, id DESC);
-- Endpoint filter.
CREATE INDEX webhook_events_endpoint_id_idx ON app.webhook_events (endpoint_id, id DESC);
-- Method filter within a project.
CREATE INDEX webhook_events_project_method_idx ON app.webhook_events (project_id, method, id DESC);
-- Retention purge by age.
CREATE INDEX webhook_events_project_received_idx ON app.webhook_events (project_id, received_at);

COMMENT ON TABLE app.webhook_events IS 'Accepted webhook requests; sensitive header and query values redacted before insert, body stored as received';

-- +goose Down
DROP TABLE app.webhook_events;
ALTER TABLE app.endpoints DROP CONSTRAINT endpoints_id_project_key;
