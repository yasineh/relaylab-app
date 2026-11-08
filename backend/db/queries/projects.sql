-- Every query is scoped by owner_id: there is deliberately no query that loads
-- a project by id alone (ownership is enforced in the data layer too).

-- name: CreateProject :one
INSERT INTO app.projects (id, owner_id, name, description)
VALUES ($1, $2, $3, $4)
RETURNING id, owner_id, name, description, created_at, updated_at;

-- name: ListProjects :many
SELECT id, owner_id, name, description, created_at, updated_at
FROM app.projects
WHERE owner_id = $1
ORDER BY created_at DESC, id DESC;

-- name: GetProject :one
SELECT id, owner_id, name, description, created_at, updated_at
FROM app.projects
WHERE id = $1 AND owner_id = $2;

-- name: UpdateProject :one
-- NULL arguments keep the current value; set_description distinguishes
-- "leave unchanged" from "clear the description".
UPDATE app.projects
SET name        = COALESCE(sqlc.narg(name), name),
    description = CASE WHEN sqlc.arg(set_description)::boolean THEN sqlc.narg(description) ELSE description END,
    updated_at  = now()
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id)
RETURNING id, owner_id, name, description, created_at, updated_at;

-- name: DeleteProject :execrows
-- One statement; everything scoped to the project goes by FK cascade.
DELETE FROM app.projects
WHERE id = $1 AND owner_id = $2;

-- name: CountProjects :one
SELECT count(*)::int AS count
FROM app.projects
WHERE owner_id = $1;

-- name: LockOwnerProjects :exec
-- Serialises creates per owner for the limit check (the user row lives in the
-- auth schema, where the api cannot take row locks).
SELECT pg_advisory_xact_lock(hashtextextended('projects:' || sqlc.arg(owner_id)::text, 0));
