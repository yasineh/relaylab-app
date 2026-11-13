-- Webhook event queries. Ingestion runs these without an owner: the endpoint
-- token is the capability, and endpoint_id/project_id come from the token
-- lookup, never from the request.

-- name: InsertEvent :exec
INSERT INTO app.webhook_events (
  id, endpoint_id, project_id, received_at, method, path, raw_query, query_params,
  headers, redacted_header_count, content_type, body, body_size, body_encoding,
  body_json, json_invalid, source_ip_prefix, user_agent, correlation_id, source, replay_attempt_id
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8,
  $9, $10, $11, $12, $13, $14,
  $15, $16, $17, $18, $19, sqlc.arg(source), sqlc.narg(replay_attempt_id)
);

-- name: TouchEndpointReceived :exec
-- Never moves last_received_at backwards when requests commit out of order.
UPDATE app.endpoints
SET last_received_at = GREATEST(COALESCE(last_received_at, sqlc.arg(received_at)), sqlc.arg(received_at))
WHERE id = sqlc.arg(id);

-- name: NotifyEvent :exec
-- Delivered to listeners only when the surrounding transaction commits.
SELECT pg_notify('events', sqlc.arg(payload)::text);

-- Explorer queries (add-event-explorer). Every query is owner-scoped by joining
-- app.projects (owner_id = the session user).
--
-- after_id and before_id are exclusive id bounds that are always set: the
-- service folds the cursor and the received-time filters into them, using the
-- nil and max UUIDs when unbounded. Keeping them non-NULL lets a cached
-- generic plan still use (project_id, id) and (endpoint_id, id) as index
-- conditions. The NULL-or optional filters below only narrow rows inside that
-- range. received_after is inclusive and received_before exclusive.
-- The ASC and DESC variants differ only in ORDER BY. The Endpoint variants
-- check the project through the endpoint (en.project_id) instead of
-- e.project_id: the endpoint determines the project (composite foreign key),
-- but the planner treats the two columns as independent, so filtering both
-- underestimates rows ~10x and can pick a read-everything-then-sort plan
-- (TestListQueriesUseIndexes).

-- name: ListEventsDesc :many
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.content_type, e.body_size
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
JOIN app.endpoints en ON en.id = e.endpoint_id
WHERE e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND e.id > sqlc.arg(after_id) AND e.id < sqlc.arg(before_id)
  AND (cardinality(sqlc.arg(methods)::text[]) = 0 OR e.method = ANY(sqlc.arg(methods)::text[]))
  AND (sqlc.narg(received_after)::timestamptz IS NULL OR e.received_at >= sqlc.narg(received_after)::timestamptz)
  AND (sqlc.narg(received_before)::timestamptz IS NULL OR e.received_at < sqlc.narg(received_before)::timestamptz)
  AND (sqlc.narg(content_type_pattern)::text IS NULL OR e.content_type ILIKE sqlc.narg(content_type_pattern)::text ESCAPE '\')
ORDER BY e.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListEventsAsc :many
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.content_type, e.body_size
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
JOIN app.endpoints en ON en.id = e.endpoint_id
WHERE e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND e.id > sqlc.arg(after_id) AND e.id < sqlc.arg(before_id)
  AND (cardinality(sqlc.arg(methods)::text[]) = 0 OR e.method = ANY(sqlc.arg(methods)::text[]))
  AND (sqlc.narg(received_after)::timestamptz IS NULL OR e.received_at >= sqlc.narg(received_after)::timestamptz)
  AND (sqlc.narg(received_before)::timestamptz IS NULL OR e.received_at < sqlc.narg(received_before)::timestamptz)
  AND (sqlc.narg(content_type_pattern)::text IS NULL OR e.content_type ILIKE sqlc.narg(content_type_pattern)::text ESCAPE '\')
ORDER BY e.id ASC
LIMIT sqlc.arg(row_limit);

-- name: ListEndpointEventsDesc :many
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.content_type, e.body_size
FROM app.webhook_events e
JOIN app.endpoints en ON en.id = e.endpoint_id
JOIN app.projects p ON p.id = en.project_id
WHERE e.endpoint_id = sqlc.arg(endpoint_id) AND en.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND e.id > sqlc.arg(after_id) AND e.id < sqlc.arg(before_id)
  AND (cardinality(sqlc.arg(methods)::text[]) = 0 OR e.method = ANY(sqlc.arg(methods)::text[]))
  AND (sqlc.narg(received_after)::timestamptz IS NULL OR e.received_at >= sqlc.narg(received_after)::timestamptz)
  AND (sqlc.narg(received_before)::timestamptz IS NULL OR e.received_at < sqlc.narg(received_before)::timestamptz)
  AND (sqlc.narg(content_type_pattern)::text IS NULL OR e.content_type ILIKE sqlc.narg(content_type_pattern)::text ESCAPE '\')
ORDER BY e.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListEndpointEventsAsc :many
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.content_type, e.body_size
FROM app.webhook_events e
JOIN app.endpoints en ON en.id = e.endpoint_id
JOIN app.projects p ON p.id = en.project_id
WHERE e.endpoint_id = sqlc.arg(endpoint_id) AND en.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND e.id > sqlc.arg(after_id) AND e.id < sqlc.arg(before_id)
  AND (cardinality(sqlc.arg(methods)::text[]) = 0 OR e.method = ANY(sqlc.arg(methods)::text[]))
  AND (sqlc.narg(received_after)::timestamptz IS NULL OR e.received_at >= sqlc.narg(received_after)::timestamptz)
  AND (sqlc.narg(received_before)::timestamptz IS NULL OR e.received_at < sqlc.narg(received_before)::timestamptz)
  AND (sqlc.narg(content_type_pattern)::text IS NULL OR e.content_type ILIKE sqlc.narg(content_type_pattern)::text ESCAPE '\')
ORDER BY e.id ASC
LIMIT sqlc.arg(row_limit);

-- name: GetEvent :one
-- The parsed body_json copy is not returned: clients parse the exact body text,
-- which keeps key order and duplicates. source_ip_prefix is returned as text
-- ("203.0.113.0/24", or "" when none). Ingestion stores the truncated network
-- address without a mask (it reads as /32 or /128), so the /24 or /48 it was
-- truncated to is applied here.
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.raw_query, e.query_params, e.headers, e.redacted_header_count, e.content_type,
       e.body, e.body_size, e.body_encoding, (e.body_json IS NOT NULL)::boolean AS is_json, e.json_invalid,
       COALESCE(set_masklen(e.source_ip_prefix, CASE family(e.source_ip_prefix) WHEN 4 THEN 24 ELSE 48 END)::text, '')::text AS source_ip_prefix, e.user_agent, e.correlation_id
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
JOIN app.endpoints en ON en.id = e.endpoint_id
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- name: GetEventBody :one
SELECT e.body
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- Live stream queries (add-live-event-stream), owner-scoped like the explorer.

-- name: GetEventSummaries :many
-- Summaries for ids the broker delivered. Ids from other projects or owners
-- are silently left out.
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.content_type, e.body_size
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
JOIN app.endpoints en ON en.id = e.endpoint_id
WHERE e.id = ANY(sqlc.arg(ids)::uuid[]) AND e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
ORDER BY e.received_at, e.id;

-- name: GetEventReceivedAt :one
-- Resolves a Last-Event-ID. No row means it is not this owner's project's event.
SELECT e.received_at
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- name: ListEventsReceivedSince :many
-- Resume backfill: events received at or after since, oldest first, using
-- (project_id, received_at).
SELECT e.id, e.endpoint_id, en.name AS endpoint_name, e.received_at, e.method, e.path,
       e.content_type, e.body_size
FROM app.webhook_events e
JOIN app.projects p ON p.id = e.project_id
JOIN app.endpoints en ON en.id = e.endpoint_id
WHERE e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
  AND e.received_at >= sqlc.arg(since)
ORDER BY e.received_at, e.id
LIMIT sqlc.arg(row_limit);
