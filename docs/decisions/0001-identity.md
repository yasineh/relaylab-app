# D1. Better Auth for identity, sessions validated by Go through the database

Status: Accepted

## Context

RelayLab needs sign-up, sign-in, sessions, email verification, and password reset. The API is written in Go, and every dashboard request must know who is calling without slowing down or depending on another service for each request.

## Decision

Use Better Auth (TypeScript), pinned to an exact version, as a separate auth service. The Go API validates sessions itself: it checks the signed `session_token` cookie with the Better Auth secret, then reads `auth.session` and `auth.user` through a read-only role. Better Auth's cookie cache is off, so revoking a session takes effect immediately. Calling Better Auth's `get-session` on every request was rejected: an extra hop per request, and a Node outage would take the whole API down.

## Consequences

- Revocation, expiry, and sign-out apply on the next request.
- Go depends on Better Auth's cookie and table format; a contract test (`auth-contract`) runs the real service against the Go validator and fails on drift.
- Two runtimes (Go and Node) to build, scan, and deploy.

## See also

- [Authentication architecture](../architecture/auth.md)
