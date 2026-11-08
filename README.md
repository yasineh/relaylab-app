<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="web/src/assets/brand/logo-horizontal-dark.svg">
    <img src="web/src/assets/brand/logo-horizontal-light.svg" alt="RelayLab" width="300">
  </picture>
</h1>

A developer workspace to receive, inspect, replay, and debug HTTP webhooks.

> **Status: early development.** The platform foundation and accounts are in
> place: Go API and worker, PostgreSQL with migrations, health checks,
> structured logging, and email/password accounts (sign-up, email verification,
> sign-in, password reset, account settings) through a Better Auth service, and
> user-owned projects. Endpoints, webhook ingestion, the event explorer, and
> replay are planned and specified but **not implemented yet**. RelayLab is not deployed anywhere.

## Run it locally

Requires pnpm, Node.js 24+, Go 1.27+, and Docker.

```sh
cp .env.example .env
pnpm install
pnpm dev        # Postgres + Mailpit + migrations, then web, API, worker, and auth
```

Open <http://localhost:5173> and create an account at `/sign-up`; the
verification email arrives in Mailpit at <http://localhost:8025>. `pnpm test`, `pnpm lint`, and `pnpm build` also
run from the root. See the [development guide](docs/development.md) for all
commands, configuration, and the Docker-only workflow (`pnpm dev:docker`).

## Documentation

- [Development guide](docs/development.md)
- [Continuous integration](docs/ci.md)
- [Architecture overview](docs/architecture/overview.md) (current implementation)
- [Database schema](docs/architecture/erd.md)
- [Brand and design system](docs/design/design-system.md)
- [API reference](docs/api.md)
- [Authentication and sessions](docs/security/authentication.md)
- [MVP plan and decisions](docs/plan/mvp-plan.md)
- [Specifications](openspec/): planned changes live in `openspec/changes/`

## Stack

Go (chi, pgx, goose, sqlc, slog) · PostgreSQL · React + TypeScript + Vite ·
Tailwind CSS + shadcn/ui · TanStack Query · pnpm workspace · Docker Compose.
