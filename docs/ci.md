# Continuous integration

CI runs on GitHub Actions (`.github/workflows/ci.yml`) **only when someone
starts it manually**, on every branch including `main`. Pushes, pull requests,
and Dependabot pull requests never trigger it. A newer manual run on the same branch cancels an older one that is still
running.

## Run CI

From the terminal (any branch that exists on GitHub):

```sh
git push                                   # the branch must be on GitHub first
gh workflow run ci.yml --ref <branch>      # start the run
gh run watch                               # follow it; exits non-zero if it fails
```

Or in GitHub: **Actions → CI → Run workflow**, then pick the branch.

The result to look at is the **`ci-success`** job: it is green only if every job
passed.

## Jobs

| Job | Fails when |
|---|---|
| **Backend lint** | gofmt would change a file, `go vet` or golangci-lint report issues (config: `backend/.golangci.yml`), generated sqlc code is stale (`sqlc diff`), actionlint finds a problem in any workflow, or the deploy manifests fail the offline check (`deploy/render.sh --check`) |
| **Backend test** | Any Go test fails (race detector, real PostgreSQL 17 service). This includes `TestMigrateUpDownUp`, which runs goose up → down → up on a fresh database. Also fails if either binary does not build |
| **Frontend** | Prettier, oxlint, TypeScript (`tsc -b`, strict), Vitest, or the production build fails |
| **Auth service** | Prettier, oxlint, TypeScript, or the Vitest suite (against a real PostgreSQL) fails for `auth/`, or Better Auth migrations fail on a fresh database or are not idempotent |
| **Auth contract** | The real auth service, the Go `/api/auth` proxy, and the Go session validator disagree: sign-in, sign-out, revoke-all, password change, forced expiry, tampered cookies, unverified users, rate limiting, or reset enumeration (`backend/scripts/auth-contract.sh`) |
| **Security scans** | gitleaks finds a secret in the full history of the selected branch, govulncheck finds a reachable Go vulnerability, or `pnpm audit --prod` reports a high/critical advisory |
| **Container image** | Either image (Go binaries, auth service) fails to build or runs as root, or Trivy finds a fixable HIGH/CRITICAL vulnerability or a secret in it |
| **End-to-end** | Prettier, oxlint, or TypeScript fails for `e2e/`, the e2e stack (`docker-compose.e2e.yml`) does not become healthy, or any Playwright test fails (Chromium, no retries). On failure the HTML report, traces, screenshots, and the stack's logs are uploaded as the `e2e-report` artifact. See [Testing](development/testing.md) |
| **ci-success** | Any job above failed, was cancelled, or was skipped. **This is the one result to check before merging** |

## Workflow security

- `permissions: contents: read` for the whole workflow; checkouts do not persist credentials.
- Triggered only by `workflow_dispatch`, which requires write access to the repository. There is no `pull_request_target`.
- No repository secrets are used. The Postgres service uses throwaway,
  non-secret test credentials.
- Third-party actions are pinned to full commit SHAs.
- There are no scheduled dependency updates. GitHub **Dependabot security
  updates** are enabled in the repository settings: when an advisory affects a
  dependency (Go modules, the pnpm workspace, or the pinned actions), Dependabot
  opens a pull request. It runs no tests; start CI on its branch manually
  before merging. Other version bumps are done by hand.
- Tool versions are pinned in the workflow `env` block: golangci-lint,
  govulncheck, sqlc, gitleaks, Trivy.

## Run the same checks locally

```sh
pnpm check
```

This runs the format check, lint (oxlint, `go vet`, gofmt, golangci-lint, sqlc
drift), typecheck, all tests (Go integration tests use the isolated
`postgres-test` container), and the production build. It needs Docker for
golangci-lint, sqlc, and the test database.

The security jobs can also be run locally:

```sh
pnpm secrets:scan                                                    # gitleaks over the working tree
(cd backend && go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...)
pnpm audit --prod --audit-level high
docker build -t relaylab:ci . && \
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:0.76.2 \
  image --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed relaylab:ci
```

## Before merging

Because CI does not run automatically, the merge gate is a habit, not a GitHub
setting:

1. Run `pnpm check` locally, **or** run CI on the branch and confirm `ci-success` is green.
2. For changes that touch the Dockerfile, dependencies, or workflows, prefer the
   CI run: it also performs the image build, Trivy, govulncheck, and gitleaks
   history scans.

GitHub branch protection cannot require `ci-success` here: a required check that
never starts automatically would block every pull request. Branch protection can
still require pull requests before merging and block force pushes to `main`.

## Updating pinned versions

- **Actions:** update the commit SHA and the version comment together (Dependabot security PRs do this for you).
- **Tool images and versions:** change them in the workflow `env` block and the
  matching root or backend `package.json` script, so local and CI stay identical.
