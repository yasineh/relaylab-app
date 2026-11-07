# Development guide

How to run, test, and work on RelayLab locally. Everything here reflects what is
implemented today; sections are added as features ship.

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| Docker + Docker Compose | Docker 24+ | Local stack, test database, sqlc and gitleaks images |
| Go | 1.27+ | API, worker, tests (only needed to run outside Docker) |
| Node.js + npm | Node 22+ (24 recommended) | Web app (only needed to run outside Docker) |
| make | any | Developer commands (`make help`) |

## Quick start (Docker)

```sh
cp .env.example .env
docker compose up --build
```

Then open <http://localhost:5173>. The home page shows live API readiness.

The stack starts these services:

| Service | What it does | Host port |
|---|---|---|
| `postgres` | PostgreSQL 17. On first start, `deploy/local/initdb/00_roles.sh` creates the schemas and roles | `127.0.0.1:54320` |
| `migrate` | One-shot `api migrate` using the migrator role; exits when done | — |
| `api` | Go HTTP server: `/healthz`, `/readyz`, `/api/v1/*`, and the embedded web build | `127.0.0.1:8080` |
| `worker` | Background worker. No job handlers exist yet; it exposes health only | `127.0.0.1:8081` |
| `web` | Vite dev server with hot reload. Proxies `/api`, `/h`, `/healthz`, `/readyz` to `api` | `127.0.0.1:5173` |

Check it is healthy:

```sh
curl localhost:8080/healthz   # {"status":"ok"}
curl localhost:8080/readyz    # {"status":"ready"}
```

If a port is already taken on your machine, change `API_HOST_PORT`,
`WORKER_HOST_PORT`, `WEB_HOST_PORT`, or `POSTGRES_HOST_PORT` in `.env`. If you
change `WEB_HOST_PORT`, set `APP_ORIGIN` to the same URL.

Stop with `make down` (keeps data) or `docker compose down -v` (deletes the
local database volume, which re-runs the role init on next start).

## Configuration

All configuration comes from environment variables. `.env.example` lists them
with safe, dev-only placeholders. Never commit a real `.env`.

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development`, `test`, or `production` |
| `APP_ORIGIN` | `http://localhost:5173` | Browser origin of the app. Required and HTTPS-only in production |
| `HTTP_ADDR` | `:8080` | API listen address |
| `WORKER_HEALTH_ADDR` | `:8081` | Worker health listen address |
| `DATABASE_URL` | — (required) | Runtime connection as `app_runtime` |
| `MIGRATION_DATABASE_URL` | — | Migrator connection as `app_migrator`; read only by `api migrate` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `TRUSTED_PROXY_CIDRS` | empty | CIDRs whose `X-Forwarded-For` is trusted. Empty trusts none |
| `SHUTDOWN_GRACE` | `20s` | Time allowed for in-flight work on SIGTERM |

The processes refuse to start when a variable is missing or malformed. The error
names the variable but never prints its value.

### Limits

Every product limit lives in one place, `internal/platform/config/limits.go`,
with a documented default and a `LIMIT_*` override. Overrides must be positive
integers. A test fails if a limit value is hard-coded anywhere else.

| Variable | Default | Effect |
|---|---|---|
| `LIMIT_HTTP_MAX_HEADER_BYTES` | 32768 | Larger request headers get `431 headers_too_large` |
| `LIMIT_API_MAX_BODY_BYTES` | 65536 | Larger `/api` bodies get `413 body_too_large` |

## Database: schemas, roles, migrations

RelayLab keeps its tables in the `app` schema. The `auth` schema is reserved for
the Better Auth service (added in `add-user-auth`) and is currently empty.

| Role | Can do | Used by |
|---|---|---|
| `app_migrator` | DDL on `app` | `api migrate` |
| `app_runtime` | Read/write data in `app`; no DDL | API and worker |
| `auth_migrator` | DDL on `auth` | Better Auth migrations (later) |
| `auth_runtime` | Read/write data in `auth` | Better Auth service (later) |

`deploy/sql/roles.sql` creates these schemas, roles, and grants idempotently.
It reads role passwords from `relaylab.*_password` session settings, so the file
itself never contains secrets.

Migrations are goose SQL files in `migrations/`, embedded into the API binary.
goose keeps its version table in `app.goose_db_version`.

```sh
make migrate                 # outside Docker, uses MIGRATION_DATABASE_URL from .env
docker compose run --rm migrate
```

`/readyz` returns 503 until the database is reachable and every embedded
migration is applied.

### SQL code generation

Queries live in `db/queries/*.sql`; sqlc generates typed Go code into
`internal/platform/db/store`. Run `make sqlc` after changing queries or
migrations (it uses the pinned `sqlc/sqlc` Docker image).

## Running outside Docker

Start only Postgres, then run each process from the repo root:

```sh
docker compose up -d postgres
set -a; . ./.env; set +a
go run ./cmd/api migrate
go run ./cmd/api            # http://localhost:8080
go run ./cmd/worker         # health on :8081
cd web && npm install && npm run dev   # http://localhost:5173
```

`make build` builds `bin/api` and `bin/worker` without the web app (Vite serves
it in development). `make build-embed` builds the web app and an API binary that
serves it, which is the production layout used by the `Dockerfile`.

## Tests and checks

```sh
make test-go      # Go unit + integration tests (starts an isolated test Postgres)
make test-web     # Vitest + React Testing Library
make lint         # go vet, gofmt check, oxlint, TypeScript
make secrets-scan # gitleaks over the working tree
```

Integration tests run against a separate `postgres-test` container (Compose
profile `test`, port 54321, in-memory storage). Each test creates and drops its
own database. Postgres roles are shared across a whole server, so tests must
never point at the dev database server.

For the web app:

```sh
cd web
npm run typecheck
npm run lint
npm run format:check
npm test -- --run
npm run build
```

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
cmd/api            HTTP server and `migrate` subcommand
cmd/worker         background worker (health only for now)
internal/platform  config, limits, logging, httpx, db, health
internal/webui     serves the embedded web build (`-tags embedweb`)
migrations/        goose SQL migrations (app schema)
db/queries/        sqlc queries
deploy/            local init scripts and roles.sql
web/               React + TypeScript + Vite app
openspec/          specifications and planned changes
docs/              documentation
```
