# D11. Replay behind an SSRF guard, URL replay off by default in production

Status: Accepted (amended)

## Context

Replaying to a user-supplied URL is a server-side request forgery risk.

## Decision

HTTPS only (HTTP only behind a development flag), every DNS answer and the actual socket address checked against a public-address policy, no redirects, strict timeouts, response and concurrency caps. Signature and credential headers are not forwarded; users may add headers, including `Authorization`, which are stored encrypted and erased after delivery. `REPLAY_ENABLED` defaults to false in production.

## Consequences

- URL replay cannot reach private networks, metadata services, or RelayLab itself; every §8.2 item maps to a test.
- Amended 2026-11-13, with the production configuration: `REPLAY_ENABLED` governs URL replay only; replay to the test sink (0012) is always available because it never leaves RelayLab.

## See also

- [Outbound requests and SSRF protections](../security/outbound-replay.md)
