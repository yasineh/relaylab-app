-- Used only by sqlc for code generation. In real databases these schemas are
-- created by deploy/sql/roles.sql (with owners and grants), not by migrations.
CREATE SCHEMA app;
CREATE SCHEMA auth;
