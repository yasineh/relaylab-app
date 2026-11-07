# Continuous integration

CI runs on GitHub Actions (`.github/workflows/ci.yml`) for every pull request
targeting `main` and every push to `main`. A newer push to the same pull request
cancels the older run.

## Jobs

| Job | Fails when |
|---|---|
| **Backend lint** | gofmt would change a file, `go vet` or golangci-lint report issues (config: `backend/.golangci.yml`), or generated sqlc code is stale (`sqlc diff`) |
| **Backend test** | Any Go test fails (race detector, real PostgreSQL 17 service). This includes `TestMigrateUpDownUp`, which runs goose up → down → up on a fresh database. Also fails if either binary does not build |
| **Frontend** | Prettier, oxlint, TypeScript (`tsc -b`, strict), Vitest, or the production build fails |
| **Auth service** | Format, lint, typecheck, unit tests, or Better Auth migrations fail for `auth/`. Until `add-user-auth` creates `auth/`, the job passes with a notice |
| **Auth contract** | The real auth service and the Go session validator disagree (valid, revoked, expired, tampered sessions). Same pre-auth guard |
| **Security scans** | gitleaks finds a secret (full history on `main`, the PR's commits on pull requests), govulncheck finds a reachable Go vulnerability, or `pnpm audit --prod` reports a high/critical advisory |
| **Container image** | The Docker build fails, or Trivy finds a fixable HIGH/CRITICAL vulnerability in the image |
| **ci-success** | Any job above failed, was cancelled, or was skipped. **This is the only check branch protection needs to require** |

## Workflow security

- `permissions: contents: read` for the whole workflow; checkouts do not persist credentials.
- Triggered by `pull_request`, never `pull_request_target`, so fork PRs get no secrets.
- No repository secrets are used. The Postgres service uses throwaway,
  non-secret test credentials.
- Third-party actions are pinned to full commit SHAs. Dependabot
  (`.github/dependabot.yml`) proposes weekly updates for Go modules, the pnpm
  workspace, and the pinned actions.
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
(cd backend && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...)
pnpm audit --prod --audit-level high
docker build -t relaylab:ci . && \
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:0.76.2 \
  image --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed relaylab:ci
```

## Branch protection (repository owner, one-time)

GitHub only enforces checks when branch protection is enabled. After the first
CI run on the repository:

1. Open **Settings → Branches → Add branch ruleset** (or **Add rule**) for `main`.
2. Enable **Require a pull request before merging**.
3. Enable **Require status checks to pass**, and add **`ci-success`** as the only
   required check.
4. Enable **Require branches to be up to date before merging**.
5. Optionally enable **Block force pushes**.

Do not add individual jobs as required checks; `ci-success` already covers them,
and it fails if any of them fails or is skipped.

## Updating pinned versions

- **Actions:** accept Dependabot PRs, which update both the SHA and the version comment.
- **Tool images and versions:** change them in the workflow `env` block and the
  matching root or backend `package.json` script, so local and CI stay identical.
