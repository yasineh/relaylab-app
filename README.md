# RelayLab

A developer workspace to receive, inspect, replay, and debug HTTP webhooks.

> **Status: early development.** The platform foundation is in place: Go API
> and worker, PostgreSQL with migrations, a React app shell, health checks, and
> structured logging. Accounts, projects, endpoints, webhook ingestion, the event
> explorer, and replay are planned and specified but **not implemented yet**.
> RelayLab is not deployed anywhere.

## Run it locally

```sh
cp .env.example .env
docker compose up --build
```

Open <http://localhost:5173>. See the [development guide](docs/development.md)
for configuration, tests, and running without Docker.

## Documentation

- [Development guide](docs/development.md)
- [Architecture overview](docs/architecture/overview.md) (current implementation)
- [Database schema](docs/architecture/erd.md)
- [MVP plan and decisions](docs/plan/mvp-plan.md)
- [Specifications](openspec/): planned changes live in `openspec/changes/`

## Stack

Go (chi, pgx, goose, sqlc, slog) · PostgreSQL · React + TypeScript + Vite ·
Tailwind CSS + shadcn/ui · TanStack Query · Docker Compose.
