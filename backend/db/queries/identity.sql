-- name: GetSessionByToken :one
-- The only query Go runs against Better Auth's tables: read-only session lookup
-- by the token from a signature-verified cookie (design decision 2, option A).
SELECT
  s."userId"         AS user_id,
  s."expiresAt"      AS expires_at,
  u.email            AS email,
  u.name             AS name,
  u."emailVerified"  AS email_verified
FROM auth."session" s
JOIN auth."user" u ON u.id = s."userId"
WHERE s.token = $1;
