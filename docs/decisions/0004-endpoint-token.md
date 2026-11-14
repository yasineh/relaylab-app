# D4. Endpoint tokens stored as a lookup hash and an encrypted copy

Status: Accepted

## Context

The receiving URL is the only credential for sending to an endpoint, and users need to see it again later.

## Decision

Tokens are 32 random bytes, base62. The database stores an HMAC-SHA256 hash for lookups and an AES-256-GCM ciphertext (bound to the project and endpoint ids) so the URL can be shown again. Rotation replaces both; the old URL stops working at once.

## Consequences

- A database leak alone does not reveal working URLs without the encryption key.
- Losing `ENDPOINT_TOKEN_ENC_KEY` makes URLs unshowable (rotation issues new ones).

## See also

- [Endpoints](../api.md#endpoints)
