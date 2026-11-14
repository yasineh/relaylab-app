# Release verification report

**Status: pre-deploy readiness review done; deployed review pending.**
RelayLab is **not** declared portfolio-ready. The public demo is not
deployed, by the owner's decision, so the deployment-only items below are
marked "pending deployment". There are no open critical or high findings.

- **Review:** phase 1 (readiness), 2026-11-14.
- **Code reviewed:** commit `1c3981f`, the readiness fixes on top of `main`
  at `f160cbf`.
- **Stack:** the production-mode local stack, `docker-compose.prod.yml`.
- **Checklist:** [release checklist](checklist.md).

## Public demo and deployment

| | |
|---|---|
| Public demo URL | Not deployed (deferred by owner decision) |
| Landing page URL | None yet. Verified locally at `https://localhost:8443/` |
| Readiness evidence | [Readiness review log](evidence/readiness-2026-11-14.log): 101 checks passed, 0 failed. `scripts/prod-smoke.sh`: 12 of 12 passed |
| Deployment verification | Pending deployment. No deployment has been made |
| CI/CD workflows | [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) and [`deploy.yml`](../../.github/workflows/deploy.yml), see [CI](../ci.md) and [Deployment](../deployment.md). Neither was run for this review: the owner's GitHub Actions minutes are exhausted, so every check below was run locally |

## Documentation, diagrams, and recording

- **Documentation:** the [documentation index](../README.md). Its links,
  anchors, routes, limits, and ERD are checked by `go test
  ./internal/doccheck/...`, which passed.
- **Architecture diagram:**
  [`docs/architecture/overview.md`](../architecture/overview.md).
- **ERD:** [`docs/architecture/erd.md`](../architecture/erd.md), checked
  against the migrations, including the new `00008`.
- **Product demo recording:** in the [README](../../README.md#demo) and at
  [`docs/demo/relaylab-demo.webm`](../demo/relaylab-demo.webm), 2 min 36 s,
  synthetic data.
- **Guide check:** the getting-started curl command, run as written on the
  production-mode stack, returned `{"ok":true,...}`. The event kept its path
  and query, and `Authorization` was stored as `[REDACTED]`.

## Test results

All runs are local, on 2026-11-14, against the code of commit `1c3981f`.

| Suite | Command | Result |
|---|---|---|
| Formatting, lint, type checks, Go, web, and auth tests, builds | `pnpm check` | Passed. Backend lint: 0 issues. Web: 235 of 235. Auth: 96 of 96. All Go packages ok, including database integration tests |
| Web flake check | `vitest run` five times | 235 of 235 each time (finding F3 fixed) |
| End-to-end | `pnpm --filter @relaylab/e2e e2e` against `docker-compose.e2e.yml` | 22 of 22 passed, including the disruptive tests |
| Production smoke test | `scripts/prod-smoke.sh` | 12 of 12 |
| Readiness review | `scripts/release-readiness.sh` | 101 passed, 0 failed ([log](evidence/readiness-2026-11-14.log)) |
| Query plans | `scripts/release-query-plans.sh` | Every query uses an index, at 0.07–0.32 ms on 10,000 events, with 20,000 in the table ([plans](evidence/query-plans-2026-11-14.log)) |
| Accessibility | `pnpm --filter @relaylab/e2e a11y-scan` | 0 violations of any impact on 11 screens at 375 and 1280 px ([log](evidence/a11y-2026-11-14.log)) |
| Dependencies, images, secrets | See the [dependency review](dependency-review.md) | No known vulnerability in shipped code or images. No secret in the tree or in 193 commits |

## Findings

| ID | Severity | Finding | Status |
|---|---|---|---|
| F1 | Medium | The Go runtime role (`app_runtime`) could insert, update, and delete rows in goose's migration history (`app.goose_db_version`), through the app schema's default privileges. A compromised API could make a later deploy skip or repeat migrations. The first readiness run proved it by emptying the local stack's table | **Fixed** in `1c3981f`: migration `00008` revokes write access; `TestRuntimeRoleCannotRewriteMigrationHistory`; re-verified by the readiness review |
| F2 | Low | No `Strict-Transport-Security` header. Mitigated on `*.run.app`, because `.app` is HSTS-preloaded, but a custom domain would not be | **Fixed** in `1c3981f`: production sends `max-age=31536000`; `TestStrictTransportSecurity`; re-verified with `curl -I` |
| F3 | Low | Signing out could land on "Your session expired" instead of "You have been signed out": the cache was cleared before navigating, so the route guard could redirect first. This was the web test flaky under load | **Fixed** in `1c3981f`: `leaveSession` navigates first, used by all five sign-out paths; five full web runs passed |
| F4 | Low | A webhook body that stops arriving gets the API's `408`, but through the local Caddy proxy the client sees `502`. The connection is still closed after the 10 s timeout | **Open, documented** in the [API reference](../api.md). Checking Cloud Run's front end is a deployed-phase item |
| F5 | Low | Caddy 2.11's default header limit is below the API's 32 KiB, so the local stack rejected 20–32 KiB headers itself, with a plain 431 or a reset HTTP/2 stream | **Fixed** in `1c3981f` (local stack only): the Caddyfile sets `max_header_size 64KiB`, so the API's JSON 431 decides |
| F6 | Medium (until checked) | Per-IP rate limits need `TRUSTED_PROXY_CIDRS` set for Cloud Run's front end. Otherwise every client shares the front end's address | **Pending deployment** ([first deployment checks](../deployment.md#first-deployment-checks)) |
| F7 | Low | The local-only Caddy and Mailpit images contain Go modules with known CVEs, one with no released fix | **Accepted**, local only: see the [dependency review](dependency-review.md#remaining-findings-accepted) |
| F8 | Info | Git commit metadata names the owner with a work email address | **Owner decision:** rewrite history before the repository is made public |

## Known limitations

- Not deployed. The live HTTPS, cloud IAM and secrets, live monitoring, and
  fresh-browser public demo checks have not been done.
- CI has not run this review's commits (no Actions minutes), so there is no
  CI run link.
- Redirect handling, DNS rebinding, and response caps are proven by package
  tests with controlled servers, not on the live replay path, because that
  would need an external redirecting or rebinding host.
- The keyboard review is automated: tab order and visible focus on the
  landing page (`landing.spec.ts`), plus component tests. No manual keyboard
  pass was made of every dashboard screen.

## Remaining security risks

- F6 until the first deployment is verified.
- F7 in local-only tooling.
- URL replay is off by default in production (`REPLAY_ENABLED`). Turning it
  on for the public demo relies on the SSRF guard, whose live-path checks
  passed locally (§2.3 of the log) and must be repeated on the deployed
  environment.

## M6 exit criteria

- [ ] Public demo deployed and accessible over HTTPS. *Pending: deferred by the owner; plan in the [deployment runbook](../deployment.md).*
- [ ] Landing page published. *Built and checked locally, not public.*
- [x] Product and developer documentation complete: [documentation index](../README.md).
- [x] Architecture diagram matches the implementation: [overview](../architecture/overview.md).
- [x] ERD matches the migrations: [ERD](../architecture/erd.md), checked by a test.
- [x] Backend automated tests implemented and run in CI. *Passed locally; the CI job is defined in `ci.yml`, but there is no run for this review.*
- [x] Frontend automated tests implemented and run in CI. *As above.*
- [x] Critical end-to-end workflow tested: 22 of 22.
- [ ] CI/CD pipeline operational. *Defined and linted, not run for this review.*
- [ ] At least one successful deployment through the pipeline. *Pending deployment.*
- [x] Short product demo recording: [README](../../README.md#demo).
- [x] No unresolved critical or high security risk, as of this readiness review.
- [x] Another developer can understand, run, test, and deploy from the docs: [development guide](../development/README.md), [testing](../development/testing.md), [deployment](../deployment.md).
