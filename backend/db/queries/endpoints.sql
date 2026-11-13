-- Endpoint queries. Every dashboard query is scoped to the owner by joining
-- app.projects (owner_id = the session user). Only GetEndpointByTokenHash, used
-- by public ingestion, looks endpoints up without an owner: the token is the
-- capability.

-- name: LockOwnedProject :one
-- Confirms ownership and serialises endpoint creation per project (limit check).
SELECT id FROM app.projects
WHERE id = $1 AND owner_id = $2
FOR UPDATE;

-- name: CountEndpoints :one
SELECT count(*)::int AS count FROM app.endpoints WHERE project_id = $1;

-- name: CreateEndpoint :one
INSERT INTO app.endpoints (id, project_id, name, token_hash, token_ciphertext, token_key_version)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, project_id, name, token_ciphertext, token_key_version, enabled, last_received_at, token_rotated_at, created_at, updated_at;

-- name: ListEndpoints :many
SELECT e.id, e.project_id, e.name, e.token_ciphertext, e.token_key_version, e.enabled, e.last_received_at, e.token_rotated_at, e.created_at, e.updated_at
FROM app.endpoints e
JOIN app.projects p ON p.id = e.project_id
WHERE e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id)
ORDER BY e.created_at DESC, e.id DESC;

-- name: GetEndpoint :one
SELECT e.id, e.project_id, e.name, e.token_ciphertext, e.token_key_version, e.enabled, e.last_received_at, e.token_rotated_at, e.created_at, e.updated_at
FROM app.endpoints e
JOIN app.projects p ON p.id = e.project_id
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id) AND p.owner_id = sqlc.arg(owner_id);

-- name: UpdateEndpoint :one
-- NULL arguments keep the current value.
UPDATE app.endpoints e
SET name       = COALESCE(sqlc.narg(name), e.name),
    enabled    = COALESCE(sqlc.narg(enabled), e.enabled),
    updated_at = now()
FROM app.projects p
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id)
  AND p.id = e.project_id AND p.owner_id = sqlc.arg(owner_id)
RETURNING e.id, e.project_id, e.name, e.token_ciphertext, e.token_key_version, e.enabled, e.last_received_at, e.token_rotated_at, e.created_at, e.updated_at;

-- name: RotateEndpointToken :one
UPDATE app.endpoints e
SET token_hash        = sqlc.arg(token_hash),
    token_ciphertext  = sqlc.arg(token_ciphertext),
    token_key_version = sqlc.arg(token_key_version),
    token_rotated_at  = now(),
    updated_at        = now()
FROM app.projects p
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id)
  AND p.id = e.project_id AND p.owner_id = sqlc.arg(owner_id)
RETURNING e.id, e.project_id, e.name, e.token_ciphertext, e.token_key_version, e.enabled, e.last_received_at, e.token_rotated_at, e.created_at, e.updated_at;

-- name: DeleteEndpoint :one
-- One statement; the endpoint's events and replays go by FK cascade.
DELETE FROM app.endpoints e
USING app.projects p
WHERE e.id = sqlc.arg(id) AND e.project_id = sqlc.arg(project_id)
  AND p.id = e.project_id AND p.owner_id = sqlc.arg(owner_id)
RETURNING e.id, e.name;

-- name: GetEndpointByTokenHash :one
-- Public ingestion lookup (add-webhook-ingestion): the presented token's HMAC.
-- is_demo: the project belongs to a demo user, whose endpoints have a lower
-- rate limit (add-production-readiness).
SELECT e.id, e.project_id, e.enabled, COALESCE(u."isAnonymous", false)::boolean AS is_demo
FROM app.endpoints e
JOIN app.projects p ON p.id = e.project_id
JOIN auth."user" u ON u.id = p.owner_id
WHERE e.token_hash = $1;

-- name: InsertAuditLog :exec
INSERT INTO app.audit_log (id, project_id, actor_user_id, action, resource_type, resource_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7);
