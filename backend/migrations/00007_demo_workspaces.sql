-- Demo workspaces (add-production-readiness, D16).
--
-- One row per demo user (a Better Auth anonymous user) whose workspace has
-- been seeded: the API inserts it in the same transaction as the seeded
-- project, endpoints, and events, so a row means "seeded exactly once".
-- Deletion contract: the auth service deletes expired demo users from
-- auth."user"; this row and all the user's app data go by cascade.

-- +goose Up
CREATE TABLE app.demo_workspaces (
  user_id    text PRIMARY KEY REFERENCES auth."user" (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  CONSTRAINT demo_workspaces_expiry CHECK (expires_at > created_at)
);

COMMENT ON TABLE app.demo_workspaces IS 'Seeded demo workspaces; deleted with their anonymous user';

-- +goose Down
DROP TABLE app.demo_workspaces;
