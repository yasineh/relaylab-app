# Development guide

How to run, test, and work on RelayLab locally. Everything here reflects what is
implemented today; sections are added as features ship.

Every command runs from the **repository root** through pnpm. The repo is a pnpm
workspace with two packages: `web` (React app) and `backend` (Go module; its
`package.json` wraps Go tooling).

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| pnpm | 10+ (repo pins 12.16.1 in `packageManager`) | Workspace commands |
| Node.js | 22+ (24 recommended) | Web app |
| Go | 1.27+ | API, worker, backend tests |
| Docker + Docker Compose | Docker 24+ | Postgres, the test database, sqlc and gitleaks images |

## Quick start

```sh
cp .env.example .env
pnpm install
pnpm dev
```

`pnpm dev` starts Postgres in Docker, applies migrations, then runs three
processes in parallel with prefixed output:

| Process | What it does | URL |
|---|---|---|
| `web dev` | Vite dev server with hot reload. Proxies `/api`, `/h`, `/healthz`, `/readyz` to the API | <http://localhost:5173> |
| `backend dev:api` | Go HTTP server: `/healthz`, `/readyz`, `/api/v1/*` | <http://localhost:8080> |
| `backend dev:worker` | Background worker. No job handlers exist yet; it exposes health only | <http://localhost:8081> |

Open <http://localhost:5173>; the home page shows live API readiness. Press
Ctrl-C to stop all three. Postgres keeps running; stop it with `pnpm db:down`.

Run only one side with `pnpm dev:web` or `pnpm dev:backend`.

```sh
curl localhost:8080/healthz   # {"status":"ok"}
curl localhost:8080/readyz    # {"status":"ready"}
```

### Everything in Docker instead

```sh
pnpm dev:docker   # docker compose up --build
```

This runs `postgres`, a one-shot `migrate`, `api`, `worker`, and `web` (Vite in
a container) from the production image layout.

### Ports

If a port is taken on your machine, change it in `.env`:

| Variable | Default | Used by |
|---|---|---|
| `WEB_HOST_PORT` | 5173 | Vite (natively and in Docker). Keep `APP_ORIGIN` in sync |
| `API_HOST_PORT` | 8080 | API container port mapping (`pnpm dev` uses `HTTP_ADDR`) |
| `WORKER_HOST_PORT` | 8081 | Worker container port mapping (`pnpm dev` uses `WORKER_HEALTH_ADDR`) |
| `POSTGRES_HOST_PORT` | 54320 | Dev database |
| `POSTGRES_TEST_HOST_PORT` | 54321 | Integration-test database |

## Commands

| Command | What it does |
|---|---|
| `pnpm dev` | Postgres + migrations, then web, api and worker with hot reload |
| `pnpm dev:docker` | The whole stack in Docker Compose |
| `pnpm db:up` / `pnpm db:down` | Start or stop the dev Postgres container |
| `pnpm migrate` | Apply migrations with the migrator role |
| `pnpm test` | Web tests (Vitest + React Testing Library) and Go unit + integration tests |
| `pnpm lint` | oxlint for web; `go vet` and the gofmt check for backend |
| `pnpm typecheck` | TypeScript (`tsc -b`) for web |
| `pnpm format` / `pnpm format:check` | Prettier for web; gofmt for backend |
| `pnpm build` | Production web build and an API binary that embeds it (`backend/bin/`) |
| `pnpm check` | Format check, lint, typecheck, tests and build: run before pushing |
| `pnpm sqlc` | Regenerate typed Go code from `backend/db/queries` |
| `pnpm secrets:scan` | gitleaks over the working tree |

Package-level scripts also work, for example
`pnpm --filter @relaylab/web test:watch`.

## Configuration

All configuration comes from environment variables. `.env.example` lists them
with safe, dev-only placeholders; never commit a real `.env`. The backend
scripts load the root `.env` through `backend/scripts/with-env.sh`, and Vite
reads it in `web/vite.config.ts`. Production gets configuration from the
deployment environment, never a file.

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development`, `test`, or `production` |
| `APP_ORIGIN` | `http://localhost:5173` | Browser origin of the app. Required and HTTPS-only in production |
| `HTTP_ADDR` | `:8080` | API listen address |
| `WORKER_HEALTH_ADDR` | `:8081` | Worker health listen address |
| `DATABASE_URL` | — (required) | Runtime connection as `app_runtime` |
| `MIGRATION_DATABASE_URL` | — | Migrator connection as `app_migrator`; read only by `migrate` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `TRUSTED_PROXY_CIDRS` | empty | CIDRs whose `X-Forwarded-For` is trusted. Empty trusts none |
| `SHUTDOWN_GRACE` | `20s` | Time allowed for in-flight work on SIGTERM |

The processes refuse to start when a variable is missing or malformed. The error
names the variable but never prints its value.

### Limits

Every product limit lives in one place,
`backend/internal/platform/config/limits.go`, with a documented default and a
`LIMIT_*` override. Overrides must be positive integers. A test fails if a limit
value is hard-coded anywhere else.

| Variable | Default | Effect |
|---|---|---|
| `LIMIT_HTTP_MAX_HEADER_BYTES` | 32768 | Larger request headers get `431 headers_too_large` |
| `LIMIT_API_MAX_BODY_BYTES` | 65536 | Larger `/api` bodies get `413 body_too_large` |

## Database: schemas, roles, migrations

RelayLab keeps its tables in the `app` schema. The `auth` schema is reserved for
the Better Auth service (added in `add-user-auth`) and is currently empty.

| Role | Can do | Used by |
|---|---|---|
| `app_migrator` | DDL on `app` | `pnpm migrate` |
| `app_runtime` | Read/write data in `app`; no DDL | API and worker |
| `auth_migrator` | DDL on `auth` | Better Auth migrations (later) |
| `auth_runtime` | Read/write data in `auth` | Better Auth service (later) |

`deploy/sql/roles.sql` creates these schemas, roles, and grants idempotently.
It reads role passwords from `relaylab.*_password` session settings, so the file
itself never contains secrets. The dev Postgres container runs it on first start
via `deploy/local/initdb/00_roles.sh`.

Migrations are goose SQL files in `backend/migrations/`, embedded into the API
binary. goose keeps its version table in `app.goose_db_version`. `/readyz`
returns 503 until the database is reachable and every embedded migration is
applied.

To reset the dev database, run `docker compose down -v` (deletes the volume), then `pnpm dev`.

### SQL code generation

Queries live in `backend/db/queries/*.sql`; sqlc generates typed Go code into
`backend/internal/platform/db/store`. Run `pnpm sqlc` after changing queries or
migrations. It uses the pinned `sqlc/sqlc` Docker image.

## Tests

`pnpm test` runs both suites. Go integration tests use a separate
`postgres-test` container (Compose profile `test`, port 54321, in-memory
storage), which the backend test script starts automatically. Each test creates
and drops its own database. Postgres roles are shared across a whole server, so
tests never point at the dev database server. With `RELAYLAB_REQUIRE_DB=1`, set
by the script, database tests fail instead of skipping when no database is
available.

## Logging and errors

- Logs are one JSON object per line with UTC timestamps, `service`, `env`, and
  `request_id`. Keys that look like credentials (authorization, cookie, token,
  secret, password, session, ...) are dropped.
- Access logs record the route pattern (never the raw path or query string),
  status, duration, and size. Headers and bodies are never logged.
- Every response carries `X-Request-ID`. Safe inbound IDs (1–64 of
  `[A-Za-z0-9._-]`) are reused; anything else is replaced.
- API errors always use
  `{"error":{"code":"...","message":"...","request_id":"..."}}` and never
  contain stack traces, SQL, or configuration.

## Repository layout

```
package.json, pnpm-workspace.yaml   workspace root: run everything from here
backend/                            Go module `relaylab`
  cmd/api                           HTTP server and `migrate` subcommand
  cmd/worker                        background worker (health only for now)
  internal/platform                 config, limits, logging, httpx, db, health
  internal/webui                    serves the embedded web build (`-tags embedweb`)
  migrations/                       goose SQL migrations (app schema)
  db/queries/                       sqlc queries
  scripts/                          env loader and test runner used by package scripts
web/                                React + TypeScript + Vite app
deploy/                             roles.sql and local Postgres init
openspec/                           specifications and planned changes
docs/                               documentation
```
