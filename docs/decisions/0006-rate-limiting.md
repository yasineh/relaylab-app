# D6. In-process rate limiting, approximate across instances

Status: Accepted

## Context

Ingestion, replay creation, and live streams need limits. A shared store (Redis) would make them exact across instances.

## Decision

Use in-process token buckets per instance and document that limits multiply by the number of instances. Cap the app at 3 instances in production.

## Consequences

- No extra infrastructure; limits are approximate (at most 3x) in production.
- Better Auth's own limits are database-backed and exact.

## See also

- [Limits](../security.md#limits)
