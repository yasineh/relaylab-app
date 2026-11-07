-- Initial migration. The app schema and roles are created by deploy/sql/roles.sql;
-- this migration only establishes goose versioning in the app schema and creates
-- no domain tables (bootstrap-foundation).

-- +goose Up
COMMENT ON SCHEMA app IS 'RelayLab application schema (managed by goose migrations)';

-- +goose Down
COMMENT ON SCHEMA app IS NULL;
