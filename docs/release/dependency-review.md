# Dependency, license, and personal-data review

Done on 2026-11-14, before the
repository is made public. Results were recorded against the change's
working tree, on top of commit `cedd86c`.

## Summary

| Check | Tool | Result |
|---|---|---|
| Go vulnerabilities | govulncheck | None |
| npm vulnerabilities | `pnpm audit` | None |
| App image (`Dockerfile`) | Trivy, vulnerabilities and secrets | None |
| Auth image (`auth/Dockerfile`) | Trivy, vulnerabilities and secrets | None |
| Local-stack images | Trivy | PostgreSQL, PgBouncer, certificate generator: none. Caddy and Mailpit: findings in their upstream binaries, below |
| Dockerfiles, compose files, Cloud Run manifests | Trivy config | None after the reviewed exceptions in `.trivyignore.yaml` |
| Workflows | actionlint | None |
| Secrets, working tree | gitleaks | None |
| Secrets, all 193 commits | gitleaks | None |
| Production dependency licenses | `pnpm licenses`, go-licenses | MIT, ISC, Apache-2.0, BSD-3-Clause, MIT-0: all permissive |
| Personal details, working tree | search | None, apart from the copyright line in `LICENSE` |
| Personal details, history | search | Commit author metadata only: see below |

## What changed

- **Go:** pgx 5.13.0, goose 3.30.1, yaml 3.0.6, and indirect modules; Go 1.27.4.
- **npm:** Better Auth 1.7.23 (its auth tables are identical to those of 1.7.18, compared column by column), Playwright 1.65.0, Vite 8.3.8, nodemailer 10.5.0, and patch updates. `@types/node` stays on 24 to match the Node 24 runtime.
- **Images:**
  - The app and echo images use distroless Debian 13. Debian 12 carried `ca-certificates` and `tzdata` advisories.
  - The auth image applies Alpine updates; the base image's zlib had CVE-2026-4738.
  - The local stacks build their PostgreSQL, PgBouncer (1.26.1), Caddy (2.11), Mailpit (1.33), and certificate-generator images from [`deploy/local/images/`](../../deploy/local/images/README.md). Each applies Alpine updates and runs as non-root, except PostgreSQL's entrypoint, which needs root to prepare its data directory before switching to the `postgres` user. PostgreSQL uses su-exec in place of gosu, a Go binary built by upstream with an old Go release.
- **CI:** its throwaway PostgreSQL service is pinned by digest.

## Remaining findings (accepted)

These are in local-only images, which are never deployed and are reachable
only on `127.0.0.1`. Each is in a Go module compiled into the upstream
binary, so removing it needs a new upstream release.

| Image | Finding | Fixed in |
|---|---|---|
| Caddy 2.11 | go-jose v3: CVE-2026-116587 (high), CVE-2026-116585 and CVE-2026-116586 (medium) | No release yet |
| Caddy 2.11 | golang.org/x/net: CVE-2026-78661 (high), CVE-2026-97045 (medium) | x/net 0.62.0 |
| Caddy 2.11 | OpenTelemetry SDK: CVE-2026-128085 (medium) | 1.47.0 |
| Caddy 2.11, Mailpit 1.33 | golang.org/x/crypto GO-2026-5932, Go stdlib CVE-2026-97054 (severity not yet rated) | Go 1.27.4, newer x/crypto |

Two misconfiguration findings are accepted in `.trivyignore.yaml`, each with
its reason:

- **The Cloud Run migration jobs have no Kubernetes security context.** Cloud
  Run does not apply one, and the images run as non-root, which CI checks.
- **PostgreSQL's entrypoint starts as root.**

## Personal details

- **Working tree:**
  - Every email address uses a reserved example domain (`example.com`,
    `.test`, `.localhost`).
  - No local machine path or public IP address appears.
  - The README and the archived CI design name the repository's GitHub
    owner, as its URL does.
  - Links to the owner's private Notion workspace were removed.
- **Media:** the demo video's metadata names only its encoder. The
  screenshots and SVGs carry no EXIF, XMP, or editor metadata.
- **History:** no commit's content ever contained a personal email address
  or local path. The private Notion links were in earlier versions of the
  MVP plan. Commit **author metadata** names the owner with a work email
  address on most commits. Removing it needs a one-time history rewrite and
  a force push, which is the owner's decision.

## How to repeat

```sh
(cd backend && go run golang.org/x/vuln/cmd/govulncheck@latest ./...)
pnpm audit
docker build -t relaylab:scan . && docker build -f auth/Dockerfile -t relaylab-auth:scan .
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:0.76.2 \
  image --scanners vuln,secret relaylab:scan     # and relaylab-auth:scan
docker run --rm -v "$PWD:/repo" -w /repo aquasec/trivy:0.76.2 \
  config --ignorefile .trivyignore.yaml --skip-dirs node_modules .
docker run --rm -v "$PWD:/repo" zricethezav/gitleaks:v8.30.1 dir /repo --config /repo/.gitleaks.toml
```
