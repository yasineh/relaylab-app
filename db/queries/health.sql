-- name: Ping :one
-- Readiness probe: a trivial round trip using the runtime role.
SELECT 1::int AS ok;
