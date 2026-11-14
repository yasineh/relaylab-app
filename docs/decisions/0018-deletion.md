# D18. Permanent deletion through database cascades

Status: Accepted

## Context

Deleting a project must remove everything in it, without orphans and without long application-side loops.

## Decision

Project deletion is permanent and cascades in one transaction to endpoints, events, replay attempts, audit entries, and jobs. Workers treat rows that disappear mid-job as finished without error.

## Consequences

- No orphaned data; a deletion-contract test fails for any `app` table with an uncascaded project reference.
- No undo.

## See also

- [Data retention and deletion](../security/data-retention.md)
