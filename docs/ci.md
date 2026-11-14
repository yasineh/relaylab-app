# Checks before merging

This repository ships no hosted CI configuration. The merge gate is a set of
local commands, each a single step, that cover formatting, linting, type
checks, every test suite, the builds, and the security scans. Any CI service
can run the same commands.

## Every change

```sh
pnpm check
```

This runs the format check, lint (oxlint, `go vet`, gofmt, golangci-lint,
sqlc drift), type checks, all unit and integration tests (Go integration
tests use the isolated `postgres-test` container), and the production
builds. It needs Docker for golangci-lint, sqlc, and the test database.

| Part | Fails when |
|---|---|
| **Backend lint** | gofmt would change a file, `go vet` or golangci-lint report issues (config: `backend/.golangci.yml`), or generated sqlc code is stale (`sqlc diff`) |
| **Backend test** | Any Go test fails (race detector, real PostgreSQL 17). This includes `TestMigrateUpDownUp`, which runs goose up → down → up on a fresh database, and the documentation checks: relative links and anchors, API route coverage, the docs index, and the generated limits table |
| **Frontend** | Prettier, oxlint, TypeScript (`tsc -b`, strict), Vitest, or the production build fails |
| **Auth service** | Prettier, oxlint, TypeScript, or the Vitest suite (against a real PostgreSQL) fails for `auth/` |

## Changes to behaviour, the UI, or the stack

```sh
pnpm test:contract               # the real auth service, Go proxy, and session validator agree; ERD matches the schema
pnpm e2e                         # the browser suite against docker-compose.e2e.yml
scripts/check-mermaid.sh         # every Mermaid diagram renders
pnpm deploy:check                # the Cloud Run manifests pass the offline check
```

See [Testing](development/testing.md) for the end-to-end suite.

## Dependencies, Dockerfiles, or images

```sh
pnpm secrets:scan                                                    # gitleaks over the working tree
(cd backend && go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...)
pnpm audit --prod --audit-level high
docker build -t relaylab:check . && \
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:0.76.2 \
  image --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed relaylab:check
```

Repeat the image scan for `auth/Dockerfile`. Both images must run as
non-root users: `docker inspect --format '{{.Config.User}}' <image>` must not
be empty or `root`. The [dependency review](release/dependency-review.md)
records the latest full scan.
