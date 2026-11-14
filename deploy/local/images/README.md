# Local-only images

Images used only by the local Docker Compose stacks (development, end-to-end,
and the production-mode stack). None of them is deployed: production uses
Neon or Cloud SQL, Neon's pooler, and Cloud Run's TLS front end.

Each starts from a pinned upstream image and applies the Alpine security
updates released after it (`apk upgrade`), so Trivy finds no fixable
vulnerability in their operating-system packages. Findings inside the Caddy
and Mailpit binaries (Go modules compiled in upstream) need an upstream
release; they are listed in `docs/release/dependency-review.md`. Rebuild with `docker compose ... build` to pick up new
updates; bump the pinned digests deliberately
(`docker buildx imagetools inspect <image:tag>`).

| File | Used for |
|---|---|
| `postgres.Dockerfile` | PostgreSQL 17 in every stack |
| `pgbouncer.Dockerfile` | The transaction-mode pooler of the production-mode stack |
| `caddy.Dockerfile` | TLS in front of the production-mode stack |
| `openssl.Dockerfile` | One-shot certificate generator of the production-mode stack |
| `mailpit.Dockerfile` | The local mail catcher (verification and password-reset emails) |
