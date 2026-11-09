-- Webhook event queries. Ingestion runs these without an owner: the endpoint
-- token is the capability, and endpoint_id/project_id come from the token
-- lookup, never from the request.

-- name: InsertEvent :exec
INSERT INTO app.webhook_events (
  id, endpoint_id, project_id, received_at, method, path, raw_query, query_params,
  headers, redacted_header_count, content_type, body, body_size, body_encoding,
  body_json, json_invalid, source_ip_prefix, user_agent, correlation_id
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8,
  $9, $10, $11, $12, $13, $14,
  $15, $16, $17, $18, $19
);

-- name: TouchEndpointReceived :exec
-- Never moves last_received_at backwards when requests commit out of order.
UPDATE app.endpoints
SET last_received_at = GREATEST(COALESCE(last_received_at, sqlc.arg(received_at)), sqlc.arg(received_at))
WHERE id = sqlc.arg(id);

-- name: NotifyEvent :exec
-- Delivered to listeners only when the surrounding transaction commits.
SELECT pg_notify('events', sqlc.arg(payload)::text);
