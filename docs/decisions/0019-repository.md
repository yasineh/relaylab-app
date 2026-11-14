# D19. A neutral, owner-independent repository

Status: Accepted

## Context

The code should be publishable without hard-coded ownership or secrets.

## Decision

The Go module path is the neutral `relaylab`; no owner is hard-coded in the app; `.env.example` holds placeholders only.

## Consequences

- The repository is private for now, so the landing page shows source and documentation links only when `PUBLIC_REPO_URL` is set.

## See also

- [README](../../README.md)
