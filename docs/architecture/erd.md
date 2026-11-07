# Database schema (ERD)

This document describes the **actual** schema created by the migrations in
`migrations/` and by `deploy/sql/roles.sql`. It is updated by every change that
alters the schema.

## Current state

No domain tables exist yet. Projects, endpoints, events, and replays are added
by later changes.

## Schemas and ownership

| Schema | Owner role | Managed by | Contents today |
|---|---|---|---|
| `app` | `app_migrator` | goose migrations in `migrations/` | `goose_db_version` (migration bookkeeping) |
| `auth` | `auth_migrator` | Better Auth migrations (from `add-user-auth`) | Empty |

Nothing is created in `public`; `CREATE` on `public` is revoked.

## Tables

### `app.goose_db_version`

goose's migration history. Created and owned by `app_migrator`; `app_runtime`
can read it so `/readyz` can compare applied and embedded versions.

| Column | Type | Notes |
|---|---|---|
| `id` | integer, identity | Primary key |
| `version_id` | bigint | Migration version |
| `is_applied` | boolean | Whether the version is applied |
| `tstamp` | timestamp without time zone | When the row was written (set by goose) |

## Roles and privileges

| Role | Privileges |
|---|---|
| `app_migrator` | Owns `app`; DDL there |
| `app_runtime` | `USAGE` on `app`; `SELECT, INSERT, UPDATE, DELETE` on tables `app_migrator` creates (default privileges); `USAGE` on `auth` |
| `auth_migrator` | Owns `auth`; DDL there |
| `auth_runtime` | `USAGE` on `auth`; data access to tables `auth_migrator` creates |

## Retention and deletion

No user data is stored yet. Retention and deletion rules are documented here as
the tables that hold user data are added.
