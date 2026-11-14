-- Protect goose's version table from the runtime role (release-hardening-review).
--
-- The app schema's default privileges (deploy/sql/roles.sql) give app_runtime
-- SELECT, INSERT, UPDATE, and DELETE on every table app_migrator creates,
-- including goose's own app.goose_db_version. The API only reads it (readiness
-- reports the applied version); write access would let a compromised API make a
-- later deploy skip or repeat migrations.

-- +goose Up
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON app.goose_db_version FROM app_runtime;

-- +goose Down
GRANT INSERT, UPDATE, DELETE ON app.goose_db_version TO app_runtime;
