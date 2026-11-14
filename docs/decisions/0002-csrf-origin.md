# D2. Same-origin deployment instead of CSRF tokens and CORS

Status: Accepted

## Context

The web app, the dashboard API, and Better Auth's endpoints all use cookies. Cross-site requests must not be able to act as the user.

## Decision

Serve everything from one origin. Better Auth's `trustedOrigins` protects its own routes. The Go API requires `Origin` (or `Sec-Fetch-Site: same-origin`) to equal `APP_ORIGIN` and `Content-Type: application/json` on unsafe methods. No CORS headers are ever sent.

## Consequences

- No CSRF tokens to manage; `SameSite=Lax` cookies plus the origin and content-type checks cover cross-site requests.
- The auth service must sit behind the same origin, which shapes the deployment (a sidecar, see 0014).

## See also

- [CSRF and cross-origin requests](../architecture/auth.md#csrf-and-cross-origin-requests)
