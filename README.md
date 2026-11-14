<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="web/src/assets/brand/logo-horizontal-dark.svg">
    <img src="web/src/assets/brand/logo-horizontal-light.svg" alt="RelayLab" width="300">
  </picture>
</h1>

RelayLab gives you a URL that records every HTTP request sent to it. Inspect
the exact method, headers, query, and body of each webhook as it arrives,
then replay it to a server you control or to a built-in test sink. It is built
for developers integrating payment providers, Git hosts, and other services
that send webhooks.

## Demo

https://github.com/user-attachments/assets/13e3ad1c-13ce-42e6-8963-85acf664bf9e

A 2 min 36 s walkthrough with synthetic data: a webhook arrives, is
inspected, and is replayed to the test sink. The
[walkthrough](docs/demo/walkthrough.md) lists the steps and how to record it
again.

## Status

| Deliverable | State |
|---|---|
| Public demo | Not deployed yet. Deployment is prepared and checked locally: see the [deployment runbook](docs/deployment.md) |
| Product | Accounts, projects, endpoints, ingestion, the event explorer, live updates, replay with SSRF protections, retention, demo workspaces, and the landing page are built and tested |
| Documentation | [Documentation index](docs/README.md) |
| Architecture diagram | [Architecture overview](docs/architecture/overview.md) |
| Database schema (ERD) | [Database schema](docs/architecture/erd.md), checked against the migrations by a test |
| Automated tests | Go, web, auth service, and browser end-to-end suites: see [Testing](docs/development/testing.md) |
| Checks and deployment | [Checks before merging](docs/ci.md) (local commands) and a [deploy script](docs/deployment.md#deploying) that has not been run against a cloud project |
| Release review | Pre-deploy readiness review done with no open critical or high findings; the review on a deployed environment is pending. See the [verification report](docs/release/verification-report.md) |
| Demo recording | [Above](#demo), also in the repository as [`docs/demo/relaylab-demo.webm`](docs/demo/relaylab-demo.webm) (WebM, 1080p). The [walkthrough](docs/demo/walkthrough.md) lists the steps and how to record it again |

## How it works

1. **Receive.** Create an endpoint and point a provider, or curl, at its URL.
   Each request is stored before RelayLab answers, with credential values
   such as `Authorization` removed on arrival.
2. **Inspect.** Requests appear live. Open one to read its body as a JSON
   tree, text, or hex, with every header and query parameter, and copy it as
   curl.
3. **Replay.** Send it again to an HTTPS URL you control, through a guard
   that only reaches public addresses, or to a test sink inside RelayLab, and
   read the response.

Start with [Getting started](docs/guide/getting-started.md).

## Run it locally

Requires pnpm, Node.js 24+, Go 1.27+, and Docker.

```sh
cp .env.example .env
pnpm install
pnpm dev        # Postgres + Mailpit + migrations, then web, API, worker, and auth
```

Open <http://localhost:5173>, choose **Try the demo** or create an account
(the verification email arrives in Mailpit at <http://localhost:8025>).
`pnpm check` runs formatting, linting, type checks, all unit and integration
tests, and the build; `pnpm e2e` runs the browser suite. See the
[development guide](docs/development/README.md) for everything else.

## Documentation

- [Getting started](docs/guide/getting-started.md) and [inspecting and replaying webhooks](docs/guide/webhook-workflow.md)
- [API reference](docs/api.md)
- [Security and privacy](docs/security.md)
- [Architecture overview](docs/architecture/overview.md), [authentication](docs/architecture/auth.md), and [database schema](docs/architecture/erd.md)
- [Development guide](docs/development/README.md) and [testing](docs/development/testing.md)
- [Deployment](docs/deployment.md) and [checks before merging](docs/ci.md)
- [Decision records](docs/decisions/README.md)
- [All documentation](docs/README.md)

## Stack

Go (chi, pgx, goose, sqlc, slog), PostgreSQL, Better Auth (Node), React with
TypeScript and Vite, Tailwind CSS and shadcn/ui, TanStack Query, Playwright,
Docker Compose, and Google Cloud Run (prepared).

## License

[MIT](LICENSE)
