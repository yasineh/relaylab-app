# Development guide

How to run, test, and work on RelayLab locally. Everything here reflects what is
implemented today; sections are added as features ship.

Every command runs from the **repository root** through pnpm. The repo is a pnpm
workspace with three packages: `web` (React app), `backend` (Go module; its
`package.json` wraps Go tooling), and `auth` (the Better Auth service, Node +
TypeScript).

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| pnpm | 10+ (repo pins 12.16.1 in `packageManager`) | Workspace commands |
| Node.js | 24+ (`.nvmrc`) | Web app; the auth service runs its TypeScript directly on Node 24 |
| Go | 1.27+ | API, worker, backend tests |
| Docker + Docker Compose | Docker 24+ | Postgres, the test database, sqlc and gitleaks images |

## Quick start

```sh
cp .env.example .env
pnpm install
pnpm dev
```

`pnpm dev` starts Postgres and Mailpit in Docker, re-applies the roles and
grants, applies the auth and app migrations, then runs four processes in parallel with prefixed output:

| Process | What it does | URL |
|---|---|---|
| `web dev` | Vite dev server with hot reload. Proxies `/api`, `/h`, `/healthz`, `/readyz` to the API | <http://localhost:5173> |
| `backend dev:api` | Go HTTP server: `/healthz`, `/readyz`, `/api/v1/*`, and the `/api/auth/*` proxy | <http://localhost:8080> |
| `backend dev:worker` | Background worker. No job handlers exist yet; it exposes health only | <http://localhost:8081> |
| `auth dev` | Better Auth service (restarts on change). Reached only through the API proxy | <http://localhost:3001> (direct requests get 403) |

Open <http://localhost:5173>; the home page shows live API readiness. Press
Ctrl-C to stop all four. Postgres and Mailpit keep running; stop them with
`pnpm db:down`.

### Your first account

1. Open <http://localhost:5173/sign-up> and create an account (passwords need
   12–128 characters and must not be common).
2. Open the Mailpit inbox at <http://localhost:8025> and click the verification
   link. You are signed in and land on **Projects**.

Or create a verified account from the terminal (works even with sign-up
disabled); the password is read from stdin without echo:

```sh
pnpm create-user -- --email you@example.com --name "Your Name"
```

Run only one side with `pnpm dev:web` or `pnpm dev:backend`.

```sh
curl localhost:8080/healthz   # {"status":"ok"}
curl localhost:8080/readyz    # {"status":"ready"}
```

### Everything in Docker instead

```sh
pnpm dev:docker   # docker compose up --build
```

This runs `postgres`, `mailpit`, the one-shot `db-roles`, `auth-migrate`, and
`migrate` steps (in that order), `auth`, `api`, `worker`, and `web` (Vite in a container)
from the production image layout. The `auth` container is on the internal
network only; the API reaches it at `http://auth:3001`.

### Ports

If a port is taken on your machine, change it in `.env`:

| Variable | Default | Used by |
|---|---|---|
| `WEB_HOST_PORT` | 5173 | Vite (natively and in Docker). Keep `APP_ORIGIN` in sync |
| `API_HOST_PORT` | 8080 | API container port mapping (`pnpm dev` uses `HTTP_ADDR`) |
| `WORKER_HOST_PORT` | 8081 | Worker container port mapping (`pnpm dev` uses `WORKER_HEALTH_ADDR`) |
| `POSTGRES_HOST_PORT` | 54320 | Dev database |
| `POSTGRES_TEST_HOST_PORT` | 54321 | Integration-test database |
| `MAILPIT_SMTP_HOST_PORT` | 1025 | Mailpit SMTP (keep `SMTP_PORT` in sync for `pnpm dev`) |
| `MAILPIT_UI_HOST_PORT` | 8025 | Mailpit inbox |
| `AUTH_PORT` | 3001 | Auth service under `pnpm dev` (keep `AUTH_SERVICE_URL` in sync) |

## Commands

| Command | What it does |
|---|---|
| `pnpm dev` | Postgres, Mailpit, migrations, then web, api, worker, and auth with hot reload |
| `pnpm dev:auth` | Only the auth service |
| `pnpm dev:docker` | The whole stack in Docker Compose |
| `pnpm db:up` / `pnpm db:down` | Start or stop the dev Postgres and Mailpit containers |
| `pnpm db:roles` | Re-apply `deploy/sql/roles.sql` (roles, schemas, grants) to the dev database |
| `pnpm migrate` | Auth migrations (`auth_migrator`), then app migrations (`app_migrator`) |
| `pnpm create-user -- --email … --name …` | Create a verified account; password from stdin |
| `pnpm test` | Web tests, auth service tests, and Go unit + integration tests (including the auth contract tests when Node and `pnpm install` are available) |
| `pnpm test:contract` | Only the auth contract and auth-schema tests (real auth service + Go proxy and validator) |
| `pnpm e2e` / `pnpm e2e:down` | Build and start the end-to-end stack and run the Playwright suite; stop it ([Testing](development/testing.md)) |
| `pnpm lint` | oxlint for web, auth, and e2e; `go vet`, gofmt, golangci-lint, and sqlc drift for backend |
| `pnpm typecheck` | TypeScript for web, auth, and e2e |
| `pnpm format` / `pnpm format:check` | Prettier for web, auth, and e2e; gofmt for backend |
| `pnpm build` | Production web build and an API binary that embeds it (`backend/bin/`) |
| `pnpm check` | Format check, lint, typecheck, tests and build: run before pushing |
| `pnpm sqlc` | Regenerate typed Go code from `backend/db/queries` |
| `pnpm deploy:check` | Validate the Cloud Run manifests offline and lint every workflow ([Deployment](deployment.md)) |
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
| `DATABASE_URL` | — (required) | Runtime connection as `app_runtime`. May go through a transaction-mode pooler. In production it must require TLS (`sslmode=require` or stricter) |
| `DATABASE_DIRECT_URL` | `DATABASE_URL` | Direct connection for `LISTEN` and the retention lock; set it when `DATABASE_URL` is pooled |
| `MIGRATION_DATABASE_URL` | — | Migrator connection as `app_migrator`; read only by `migrate` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `TRUSTED_PROXY_CIDRS` | empty | CIDRs whose `X-Forwarded-For` is trusted. Empty trusts none |
| `SHUTDOWN_GRACE` | `20s` | Time allowed for in-flight work on SIGTERM. Open live event streams are closed at once so they don't hold it up |
| `STREAM_REVALIDATE_INTERVAL` | `60s` | How often a live event stream re-checks its session and project; 1s to 60s |

Authentication and mail ([details](security/authentication.md)):

| Variable | Default | Notes |
|---|---|---|
| `BETTER_AUTH_SECRET` | — (required) | Signs session cookies. Shared by the auth service and the API; 32+ characters. Rotating it signs everyone out |
| `AUTH_PROXY_SHARED_SECRET` | — (required) | Proves requests come from the API proxy; 32+ characters, different from the above |
| `AUTH_SERVICE_URL` | `http://localhost:3001` | Where the API proxies `/api/auth/*`. Required in production |
| `AUTH_SIGNUP_ENABLED` | `true`; `false` in production | Public sign-up |
| `AUTH_REQUIRE_EMAIL_VERIFICATION` | `true` | Users must verify their email before signing in |
| `AUTH_ALLOW_PUBLIC_SIGNUP_ACK` | `false` | In production, sign-up also needs this set to `true` (and email verification on); a deliberate decision |
| `AUTH_DATABASE_URL` | — (required by auth) | Auth service connection as `auth_runtime` |
| `AUTH_MIGRATION_DATABASE_URL` | — | Auth migrations as `auth_migrator` |
| `AUTH_PORT` | `3001` | Auth service listen port |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD` | — | Mail server. Required in production. Without `SMTP_HOST` outside production, emails are only logged as captured, not delivered |
| `MAIL_FROM` | `RelayLab <no-reply@relaylab.localhost>` | Sender address. Quote it in `.env` because of the `<` |

Replay, read by the API and the worker ([details](security/outbound-replay.md#replay)):

| Variable | Default | Notes |
|---|---|---|
| `REPLAY_ENABLED` | `true`; `false` in production | Turns replay to URLs on; production needs an explicit `true`. Replay to a test sink always works |
| `REPLAY_OVERRIDE_ENC_KEY` | — (required when `REPLAY_ENABLED` is on) | 32 bytes, base64. Encrypts header values users add to a replay. Without it, added headers are refused |
| `REPLAY_PROJECT_CONCURRENCY`, `REPLAY_PROJECT_QUEUE_DEPTH`, `REPLAY_USER_RATE_PER_MINUTE` | 2, 20, 30 | Running per project, waiting or running per project (`429 replay_queue_full`), creations per user per minute (`429 rate_limited`) |
| `REPLAY_MAX_TRIES`, `REPLAY_LEASE_SECONDS`, `REPLAY_MAX_LEASE_LOSSES` | 3, 60, 3 | Deliveries per replay, worker lease, lease expiries before a job is abandoned (`worker_lost`) |
| `REPLAY_RESPONSE_EXCERPT_BYTES`, `REPLAY_HISTORY_PAGE_DEFAULT` | 65536, 25 | Stored response body (at most 262144); replay history and activity page size |

Demo workspaces, read by the API, the worker, and the auth service ([details](security/authentication.md#demo-users)):

| Variable | Default | Notes |
|---|---|---|
| `DEMO_ENABLED` | `true`; `false` in production | Offer "Try the demo" (Better Auth anonymous users). Set it the same for the API and the auth service |
| `DEMO_TTL` | `24h` | A demo workspace and its user are deleted this long after creation (1m to 168h, whole minutes) |
| `DEMO_STARTS_PER_HOUR` | 10 | Demos one client IP may start per hour (auth service) |
| `DEMO_MAX_PROJECTS`, `DEMO_MAX_ENDPOINTS`, `DEMO_MAX_EVENTS`, `DEMO_MAX_STREAMS` | 1, 2, 200, 2 | Demo users' limits |
| `DEMO_INGEST_RATE_PER_MINUTE`, `DEMO_INGEST_BURST` | 10, 5 | Webhooks a demo endpoint accepts |

Data retention, read by the worker (purge) and the API (reported in `GET /api/v1/config`) ([details](security/data-retention.md)):

| Variable | Default | Notes |
|---|---|---|
| `RETENTION_EVENT_TTL` | `168h` | Events and their replays older than this are deleted. Go durations (no `d` unit); `24h` for a public demo. `0` turns off deletion by age outside production |
| `RETENTION_MAX_EVENTS_PER_PROJECT` | 10000 | Oldest events beyond this many per project are deleted on each run |
| `RETENTION_AUDIT_TTL`, `RETENTION_JOB_TTL` | `2160h`, `168h` | Audit log entries; finished (done or dead) jobs |
| `RETENTION_INTERVAL` | `10m` | Time between purge runs (1m to 24h) |

Outbound requests (replay destinations), read by the worker ([details](security/outbound-replay.md)):

| Variable | Default | Notes |
|---|---|---|
| `OUTBOUND_ALLOW_HTTP` | `false` | Allow `http://` destinations and port 80. Local development only: the worker refuses to start with it in production |
| `OUTBOUND_ALLOWED_PORTS` | `443,8443` | Destination ports allowed |
| `OUTBOUND_BLOCKED_HOSTS` | empty | Extra host names (with subdomains) that are never destinations, on top of RelayLab's own hosts, the database hosts, and internal names |
| `OUTBOUND_CONNECT_TIMEOUT`, `OUTBOUND_TLS_TIMEOUT`, `OUTBOUND_HEADER_TIMEOUT`, `OUTBOUND_TOTAL_TIMEOUT` | `3s`, `3s`, `5s`, `10s` | Each at most `1m`; the total covers the whole request including the body |
| `OUTBOUND_MAX_RESPONSE_BYTES` | 65536 | Response body kept (at most 262144); longer bodies are truncated |
| `OUTBOUND_MAX_CONCURRENCY` | 8 | Requests in flight per worker process |

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
| `LIMIT_AUTH_PROXY_TIMEOUT_SECONDS` | 10 | Proxied `/api/auth` requests that take longer get `504 auth_timeout` |
| `LIMIT_PROJECTS_PER_USER` | 20 | Creating more projects gets `409 project_limit_reached` |
| `LIMIT_STREAMS_PER_USER` | 5 | Opening more live event streams on one API instance gets `429 too_many_streams` |

## Database: schemas, roles, migrations

RelayLab keeps its tables in the `app` schema. The `auth` schema belongs to the
Better Auth service (users, sessions, credentials); see the
[ERD](architecture/erd.md).

| Role | Can do | Used by |
|---|---|---|
| `app_migrator` | DDL on `app` | `pnpm migrate` (goose) |
| `app_runtime` | Read/write data in `app`; read `auth."session"` and `auth."user"`; no DDL | API and worker |
| `auth_migrator` | DDL on `auth` | `pnpm migrate` (Better Auth migrations) |
| `auth_runtime` | Read/write data in `auth` | Auth service |

`deploy/sql/roles.sql` creates these schemas, roles, and grants idempotently.
It reads role passwords from `relaylab.*_password` session settings, so the file
itself never contains secrets. The dev Postgres container runs it on first start
via `deploy/local/initdb/00_roles.sh`.

`pnpm migrate` runs the Better Auth migrations first (they also grant
`app_runtime` its read access), then the app migrations. App migrations are
goose SQL files in `backend/migrations/`, embedded into the API binary. goose keeps its version table in `app.goose_db_version`. `/readyz`
returns 503 until the database is reachable and every embedded migration is
applied.

App tables reference `auth."user"`, so `pnpm migrate` always runs the auth
migrations first, and the app migrations refuse to start if the auth tables are
missing.

To reset the dev database, run `docker compose down -v` (deletes the volume), then `pnpm dev`.

`deploy/sql/roles.sql` is idempotent and is re-applied before every migrate:
`pnpm dev` and `pnpm dev:backend` run `pnpm db:roles`, and Docker Compose runs a
one-shot `db-roles` service before the migrations. Role and grant changes
therefore reach an existing local database without resetting it. Run
`pnpm db:roles` yourself before `pnpm migrate` if you migrate by hand.

### SQL code generation

Queries live in `backend/db/queries/*.sql`; sqlc generates typed Go code into
`backend/internal/platform/db/store`. Run `pnpm sqlc` after changing queries or
migrations. It uses the pinned `sqlc/sqlc` Docker image.

## Tests

`pnpm test` runs all three suites. The Go and auth integration tests use a
separate `postgres-test` container (Compose profile `test`, port 54321,
in-memory storage), which the test scripts start automatically. Each test file
creates and drops its own database. Postgres roles are shared across a whole
server, so tests never point at the dev database server, and concurrent suites
serialise role setup with an advisory lock. With `RELAYLAB_REQUIRE_DB=1`, set
by the scripts, database tests fail instead of skipping.

| Suite | What it covers |
|---|---|
| `web` | Components, forms, route guard, session-expiry handling. `fetch` is mocked; unmocked requests fail the test |
| `auth` | Better Auth configuration against a real database: sign-up, verification, sign-in, reset, rate limits, migrations and grants, the operator CLI, and credential-free logs |
| `backend` | Handlers, middleware, session validation, the auth proxy, projects, and database integration. Tests that need the `auth` schema use the real auth migrations when Node.js is available and a stub of the two tables Go reads otherwise |
| Auth contract (`pnpm test:contract`) | The real auth service behind the real Go proxy and validator: valid, signed-out, revoked, expired, tampered, and unverified sessions; rate limiting and enumeration through the proxy |

The contract tests need Node.js and `pnpm install`. They skip without them,
unless `RELAYLAB_REQUIRE_AUTH=1` (set by `pnpm test:contract` and the CI
`auth-contract` job).

The browser end-to-end suite (`pnpm e2e`) runs against a separate Docker
Compose stack; see [Testing](development/testing.md).

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
  internal/identity                 session validation, /api/auth proxy, /api/v1/me
  internal/projects                 project service and /api/v1/projects handlers
  internal/testutil                 deletion (no-orphans contract) and isolation (cross-user 404) helpers
  scripts/                          env loader and test runners used by package scripts
auth/                               Better Auth service (Node 24, TypeScript)
  src/                              server, Better Auth config, mailer, migrate, CLIs
  test/                             integration tests against a real database
web/                                React + TypeScript + Vite app
e2e/                                Playwright end-to-end suite and its echo destination
deploy/                             roles.sql and local Postgres init
openspec/                           specifications and planned changes
docs/                               documentation
```
