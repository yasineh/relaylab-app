# D5. A Postgres job queue, no Redis

Status: Accepted

## Context

Replays run in a background worker with retries, leases, and per-project limits. A message broker would add another stateful service.

## Decision

Use a Postgres `jobs` table claimed with `FOR UPDATE SKIP LOCKED`, leases that workers extend, bounded attempts, a dead state, and a reaper for abandoned jobs. Jobs are created in the same transaction as the replay. No Redis.

## Consequences

- Exactly-once claiming and transactional job creation without another service.
- Throughput is bounded by Postgres, ample for this product.

## See also

- [Replay flow](../architecture/overview.md#replay-flow)
