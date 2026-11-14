# Release checklist

A reusable checklist for releasing RelayLab. It covers the final hardening
review and the release definition of done. Each line is tagged:

- **readiness:** checked on the production-mode local stack
  (`docker-compose.prod.yml`) before any deployment.
- **deployed:** checked against the deployed environment.

The latest results are in the [verification report](verification-report.md).

## Severity rules

| Severity | Meaning | Release rule |
|---|---|---|
| Critical | Data of one user readable or changeable by another, remote code execution, secrets exposed, or SSRF into internal services | Blocks release |
| High | A security control missing or bypassable, such as an authorization check, a limit, or redaction; or data loss | Blocks release |
| Medium | Defence in depth missing, or a control weaker than documented, with no direct exploit | Fix or list as a known risk |
| Low | Hardening, a wrong status code, or documentation drift | Fix or list as a known limitation |

Fix small critical and high findings in the release change. A finding that
needs a new capability is planned as its own change, and the release waits
for it.

## Final hardening review

| # | Item | Phase | How |
|---|---|---|---|
| 1 | Authentication and authorization boundaries: no session, forged cookie, revoked session, origin checks | readiness, deployed | `scripts/release-readiness.sh` §2.1 |
| 2 | Project and event isolation: another user's project, endpoint, event, body, replay, audit log, and stream are `404`; their writes are refused | readiness, deployed | §2.1 |
| 3 | Ingestion limits and abuse protection: body, header count and size, body timeout, burst rate limit with `Retry-After`, unknown tokens and methods | readiness, deployed | §2.2 |
| 4 | Credential redaction on arrival, with nothing stored and nothing logged | readiness | §2.2, database and log search |
| 5 | Malformed input: invalid JSON and non-UTF-8 bodies stored byte for byte | readiness | §2.2 |
| 6 | SSRF protections on the replay path: IPv4 and IPv6, mapped and alternate encodings, private, link-local, multicast, metadata, internal names, DNS answers, ports, schemes, credentials in URLs | readiness, deployed | §2.3 with `REPLAY_ENABLED=true` |
| 7 | Redirects, DNS rebinding, the connect-time socket check, timeouts, and response caps | readiness | outbound package tests, run by §2.3 |
| 8 | Retention and deletion: expired events purged; expired demo workspaces deleted with their data; real users untouched | readiness | §2.5 |
| 9 | Database indexes: event list (both orders, filtered), event detail, and replay history use indexes on 10,000 events | readiness | `scripts/release-query-plans.sh` |
| 10 | Production configuration refuses unsafe settings and names the variable, never the value | readiness | §2.4 |
| 11 | Database roles: runtime roles cannot create tables, read password hashes, write sessions, rewrite migration history, or read the other service's data | readiness | §2.4 |
| 12 | Secret management: Secret Manager references, least-privilege service accounts, no service account keys | deployed | `gcloud` output |
| 13 | Monitoring, health checks, and error logging: metrics, request-ID log search, alerting, no bodies or secrets in logs | readiness (logs), deployed | `scripts/prod-smoke.sh`; Cloud Logging |
| 14 | Accessibility and responsiveness: axe at 375 and 1280 px on 11 screens; keyboard order | readiness | `pnpm --filter @relaylab/e2e a11y-scan`; `landing.spec.ts` |
| 15 | No development-only features or debug output: no `/debug`, no public `/metrics`, security headers including HSTS, no versioned `Server` header | readiness, deployed | §2.4 |
| 16 | Dependency and image vulnerabilities, secrets in the repository and history | readiness | [dependency review](dependency-review.md) |
| 17 | Fresh-browser public demo: start a demo, send a webhook, inspect, replay to the test sink | deployed | screenshots |
| 18 | Documentation checked against the running application | readiness, deployed | doc checks in `go test`; guide commands run |
| 19 | `TRUSTED_PROXY_CIDRS` set so per-IP limits use the client address behind Cloud Run's front end | deployed | [first deployment checks](../deployment.md#first-deployment-checks) |
| 20 | The ingestion `408` as seen through the deployed front end | deployed | stalled-body request |

## Release definition of done

| Item | Phase |
|---|---|
| Public demo on a stable HTTPS URL, or a documented blocker and plan | deployed |
| Synthetic demo data with security, rate, payload, and retention limits | readiness, deployed |
| Responsive landing page, publicly accessible | readiness, deployed |
| Product and developer documentation published | readiness |
| Architecture diagram matching the system | readiness |
| ERD matching the migrations | readiness (checked by a test) |
| Backend tests for critical functionality and security | readiness |
| Frontend tests for critical UI | readiness |
| Critical end-to-end workflow automated | readiness |
| Formatting, linting, type checks, tests, and builds run as one gate | readiness |
| The documented deploy can deploy the demo, with at least one verified deployment | deployed |
| A 2–4 minute recording of arrival, inspection, and replay with synthetic data | readiness |
| README links to the demo, docs, diagrams, ERD, and recording | readiness, deployed |
| No unresolved critical or high security issue | readiness, deployed |
| A new developer can understand, run, test, and deploy from the docs | readiness |
