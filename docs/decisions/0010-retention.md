# D10. Time-based retention with a per-project cap

Status: Accepted

## Context

Payloads may hold personal data, and storage must stay bounded, especially for a public demo.

## Decision

Delete events (and their replays) after 7 days by default, keep at most 10,000 events per project, and purge old audit entries and finished jobs, from a worker loop holding an advisory lock. A demo deployment sets 24 hours.

## Consequences

- Predictable storage and a documented privacy promise.
- The cap can be briefly exceeded between purge runs (documented).

## See also

- [Data retention and deletion](../security/data-retention.md)
