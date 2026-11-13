-- RelayLab database schemas and least-privilege roles (D15).
--
-- Idempotent: safe to run repeatedly. Run as a superuser (local) or the
-- database owner (managed Postgres: Neon's neondb_owner, Cloud SQL's
-- postgres user; both have CREATEROLE) while connected to the RelayLab
-- database.
-- Role passwords are read from session settings so this file never contains
-- secrets; set them before running, for example:
--
--   PGOPTIONS="-c relaylab.app_migrator_password=... -c relaylab.app_runtime_password=... \
--              -c relaylab.auth_migrator_password=... -c relaylab.auth_runtime_password=..." \
--   psql -v ON_ERROR_STOP=1 -d relaylab -f deploy/sql/roles.sql
--
-- Roles:
--   app_migrator   DDL on schema app (goose migrations); USAGE on auth (for FKs)
--   app_runtime    DML on schema app; USAGE on auth (SELECT grants on specific
--                  auth tables are added by the Better Auth migration step)
--   auth_migrator  DDL on schema auth (Better Auth migrations)
--   auth_runtime   DML on schema auth (Better Auth service)

DO $$
DECLARE
  r record;
BEGIN
  FOR r IN
    SELECT * FROM (VALUES
      ('app_migrator',  'relaylab.app_migrator_password'),
      ('app_runtime',   'relaylab.app_runtime_password'),
      ('auth_migrator', 'relaylab.auth_migrator_password'),
      ('auth_runtime',  'relaylab.auth_runtime_password')
    ) AS t(role_name, setting_name)
  LOOP
    IF coalesce(current_setting(r.setting_name, true), '') = '' THEN
      RAISE EXCEPTION 'session setting % must be set to the % password', r.setting_name, r.role_name;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r.role_name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L',
                     r.role_name, current_setting(r.setting_name));
    ELSIF (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
      EXECUTE format('ALTER ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L',
                     r.role_name, current_setting(r.setting_name));
    ELSE
      -- Only superusers may touch the SUPERUSER and CREATEDB attributes; the
      -- roles were created without them, so rotate the password only.
      EXECUTE format('ALTER ROLE %I LOGIN PASSWORD %L', r.role_name, current_setting(r.setting_name));
    END IF;
  END LOOP;

  -- Managed Postgres (Neon, Cloud SQL): the database owner is not a
  -- superuser. Since PostgreSQL 16, creating a role gives its creator only
  -- ADMIN on it, but handing over schema ownership and setting default
  -- privileges below needs SET on the migrator roles. Grant that to the
  -- owner running this script (without inheriting their privileges).
  IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
    EXECUTE format('GRANT app_migrator, auth_migrator TO %I WITH SET TRUE, INHERIT FALSE', current_user);
  END IF;

  EXECUTE format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database());
  EXECUTE format('GRANT CONNECT ON DATABASE %I TO app_migrator, app_runtime, auth_migrator, auth_runtime',
                 current_database());
END
$$;

-- Nobody creates objects in public.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- Schemas and owners.
CREATE SCHEMA IF NOT EXISTS app AUTHORIZATION app_migrator;
CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION auth_migrator;
ALTER SCHEMA app OWNER TO app_migrator;
ALTER SCHEMA auth OWNER TO auth_migrator;

-- Runtime access, granted by each schema's owner (works for a superuser and
-- for a managed database owner, who holds SET on the migrator roles).
SET ROLE app_migrator;
GRANT USAGE ON SCHEMA app TO app_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA app
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA app
  GRANT USAGE, SELECT ON SEQUENCES TO app_runtime;
RESET ROLE;

SET ROLE auth_migrator;
-- app_migrator needs USAGE to create foreign keys to auth."user" (REFERENCES is
-- granted on that table by the auth migrate command).
GRANT USAGE ON SCHEMA auth TO auth_runtime, app_runtime, app_migrator;
ALTER DEFAULT PRIVILEGES IN SCHEMA auth
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO auth_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA auth
  GRANT USAGE, SELECT ON SEQUENCES TO auth_runtime;
RESET ROLE;

-- Default search paths keep unqualified names inside each role's schema.
ALTER ROLE app_migrator  SET search_path = app;
ALTER ROLE app_runtime   SET search_path = app;
ALTER ROLE auth_migrator SET search_path = auth;
ALTER ROLE auth_runtime  SET search_path = auth;
