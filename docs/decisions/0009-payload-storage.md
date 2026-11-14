# D9. Store bodies exactly; redact credential values on arrival

Status: Accepted

## Context

Developers need the exact request to debug it, but webhooks carry credentials that must not be kept.

## Decision

Store the raw body as `bytea` (256 KiB default, 1 MiB hard cap) and headers up to 32 KiB. A central, configurable redactor replaces sensitive header and query values before the insert; originals are never stored, logged, or displayed, and the UI shows only that a value was removed.

## Consequences

- Byte-for-byte bodies, including binary and invalid JSON.
- Replays cannot reproduce removed credentials; users add headers explicitly (see 0011).

## See also

- [Received events](../security/events.md)
