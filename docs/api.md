# API reference

The HTTP API that exists **today**. Later changes add projects, endpoints,
events, and replay; each one documents its routes here when it ships.

Everything is served from the application origin (`APP_ORIGIN`): the web app,
the dashboard API under `/api/v1`, and authentication under `/api/auth`. The
API sends no CORS headers, so it can only be used from the RelayLab web app or
from clients that are not browsers.

## Conventions

- **Format**: JSON request and response bodies. Requests with a body to `/api/v1` must use `Content-Type: application/json`.
- **Authentication**: the session cookie set by `/api/auth`. See [Authentication and sessions](security/authentication.md).
- **CSRF**: `POST`, `PUT`, `PATCH`, and `DELETE` on `/api/v1` require `Origin: <APP_ORIGIN>`. When the browser sends no `Origin`, `Sec-Fetch-Site: same-origin` is required instead.
- **Request IDs**: every response has `X-Request-ID`. A safe inbound value (1–64 characters of `[A-Za-z0-9._-]`) is reused.
- **Limits**: request headers up to `LIMIT_HTTP_MAX_HEADER_BYTES` (32 KiB); `/api` bodies up to `LIMIT_API_MAX_BODY_BYTES` (64 KiB); proxied `/api/auth` requests are bounded by `LIMIT_AUTH_PROXY_TIMEOUT_SECONDS` (10 s).

### Error envelope

Every error from the Go API has this shape. It never contains stack traces,
SQL, file paths, or configuration values.

```json
{ "error": { "code": "unauthenticated", "message": "Sign in to continue.", "request_id": "6f1c…" } }
```

| Status | Code | When |
|---|---|---|
| 401 | `unauthenticated` | No session cookie, or it is malformed, tampered, unknown, or revoked |
| 401 | `session_expired` | The session's expiry time has passed |
| 403 | `email_not_verified` | Email verification is required and the user has not verified |
| 403 | `origin_rejected` | Unsafe method without a matching `Origin` or `Sec-Fetch-Site: same-origin` |
| 404 | `not_found` | Unknown path (on `/api/v1`, only after authentication succeeds) |
| 405 | `method_not_allowed` | Known path, wrong method |
| 413 | `body_too_large` | Body over the limit |
| 415 | `unsupported_media_type` | Body is not `application/json` |
| 431 | `headers_too_large` | Headers over the limit |
| 500 | `internal_error` | Unexpected failure (details only in server logs) |
| 502 | `auth_unavailable` | The auth service could not be reached (`/api/auth` only) |
| 504 | `auth_timeout` | The auth service did not answer in time (`/api/auth` only) |

## Dashboard API (`/api/v1`)

Every route requires a valid session except `GET /api/v1/config`. Unknown
`/api/v1` paths also require a session, so route existence cannot be probed
while signed out.

### `GET /api/v1/config`

Public settings the web app needs before sign-in. No authentication. Contains no user data.

```json
{ "signup_enabled": true, "email_verification_required": true }
```

| Field | Meaning |
|---|---|
| `signup_enabled` | Whether public sign-up is open (`AUTH_SIGNUP_ENABLED`) |
| `email_verification_required` | Whether users must verify their email before signing in (`AUTH_REQUIRE_EMAIL_VERIFICATION`) |

### `GET /api/v1/me`

The signed-in user, taken only from the validated session. Responses are
`Cache-Control: no-store`.

```json
{ "id": "Ki5Aykfh3hz7EGvU4FRKQnYeFhx1mbAg", "email": "ada@example.com", "name": "Ada Lovelace", "email_verified": true }
```

Errors: `401 unauthenticated`, `401 session_expired`, `403 email_not_verified`.

## Authentication (`/api/auth/*`)

These routes are served by Better Auth 1.7.18 through the Go API's reverse
proxy. Request and response bodies are Better Auth's own: errors use
`{ "code": "...", "message": "..." }`, not the envelope above, except for the
proxy errors `502`, `504`, and `413`. Unsafe requests must carry
`Origin: <APP_ORIGIN>`. The web app uses the official Better Auth React client.

| Method and path | Purpose | Notes |
|---|---|---|
| `POST /api/auth/sign-up/email` | Create an account: `{name, email, password, callbackURL?}` | Only when sign-up is enabled. With verification required: no session, a verification email is sent, and the response is the same for existing emails |
| `GET /api/auth/verify-email?token=…&callbackURL=…` | Emailed link: verifies the email and signs in | Redirects to `callbackURL` (the web app uses `/verify-email`), adding `?error=INVALID_TOKEN` or `TOKEN_EXPIRED` on failure |
| `POST /api/auth/send-verification-email` | Send a new verification link: `{email, callbackURL?}` | Rate limited: 3 per hour per IP |
| `POST /api/auth/sign-in/email` | Sign in: `{email, password}`; sets the session cookie | Rate limited: 10 per 5 minutes per IP. Same `401 INVALID_EMAIL_OR_PASSWORD` for wrong passwords and unknown emails; `403 EMAIL_NOT_VERIFIED` for unverified accounts |
| `GET /api/auth/get-session` | Current session, refreshing it at most once a day | Used by the web app on load and focus |
| `POST /api/auth/sign-out` | Revoke the current session and clear the cookie | Succeeds without a session too |
| `POST /api/auth/request-password-reset` | Email a reset link: `{email, redirectTo}` | Identical response for known and unknown emails. Rate limited: 3 per hour per IP |
| `GET /api/auth/reset-password/<token>?callbackURL=…` | Emailed link: checks the token | Redirects to `/reset-password?token=…`, or `?error=INVALID_TOKEN` |
| `POST /api/auth/reset-password` | Set a new password: `{newPassword, token}` | Single use, 1 hour. Revokes all sessions |
| `POST /api/auth/update-user` | Change the display name: `{name}` | 1–80 characters |
| `POST /api/auth/change-password` | `{currentPassword, newPassword, revokeOtherSessions}` | The web app always revokes other sessions |
| `POST /api/auth/revoke-sessions` | Sign out everywhere, including this session | |
| `POST /api/auth/delete-user` | Delete the account: `{password}` | Permanent |

Password rules: 12–128 characters, not on the common-password list (error code
`PASSWORD_TOO_COMMON`). Throttled requests get `429` with `Retry-After`.

## Health

| Route | Response |
|---|---|
| `GET /healthz` | `200 {"status":"ok"}` while the process is up (liveness) |
| `GET /readyz` | `200 {"status":"ready"}`, or `503` with failing checks (`database`, `migrations`) |

Health routes never read the session cookie.
