# API reference

The HTTP API that exists **today**. Later changes add webhook ingestion,
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
- **Limits**: request headers up to `LIMIT_HTTP_MAX_HEADER_BYTES` (32 KiB); `/api` bodies up to `LIMIT_API_MAX_BODY_BYTES` (64 KiB); proxied `/api/auth` requests are bounded by `LIMIT_AUTH_PROXY_TIMEOUT_SECONDS` (10 s); `LIMIT_PROJECTS_PER_USER` projects per user (20); `LIMIT_ENDPOINTS_PER_PROJECT` endpoints per project (25).

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
| 400 | `invalid_json` | The body is not one JSON object, has unknown fields, or has trailing data |
| 404 | `not_found` | Unknown path (on `/api/v1`, only after authentication succeeds), or a resource that is missing, owned by someone else, or has a malformed id. These cases are indistinguishable |
| 405 | `method_not_allowed` | Known path, wrong method |
| 413 | `body_too_large` | Body over the limit |
| 409 | `project_limit_reached` | Creating a project beyond `LIMIT_PROJECTS_PER_USER` |
| 409 | `endpoint_limit_reached` | Creating an endpoint beyond `LIMIT_ENDPOINTS_PER_PROJECT` |
| 415 | `unsupported_media_type` | Body is not `application/json` |
| 422 | `validation_failed` | A field is invalid; `error.fields` maps each field to a message |
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

### Projects

A project belongs to exactly one user: the signed-in user. Every project route
acts only on the caller's own projects; a project owned by someone else returns
the same `404 not_found` as one that does not exist. The owner is always taken
from the session, never from the request.

A project looks like this (timestamps are UTC, RFC 3339):

```json
{
  "id": "01936f3e-7b8a-7c1d-9e2f-0a1b2c3d4e5f",
  "name": "Stripe sandbox",
  "description": "Payment provider test hooks",
  "created_at": "2026-11-08T10:00:00Z",
  "updated_at": "2026-11-08T10:00:00Z"
}
```

| Field | Rules |
|---|---|
| `id` | UUIDv7, assigned by the server |
| `name` | Required. 1–80 characters after trimming whitespace; no control characters. Stored and returned as given (HTML is not interpreted). Not unique |
| `description` | Optional, may be `null`. Up to 500 characters after trimming; line breaks allowed. An empty string is stored as `null` |

| Method and path | Body | Success | Errors |
|---|---|---|---|
| `GET /api/v1/projects` | — | `200 {"items":[project, ...]}`, newest first. Bounded by the project limit, so not paginated | |
| `POST /api/v1/projects` | `{"name": "...", "description": "..."}` | `201` project, with `Location: /api/v1/projects/<id>` | `422`, `409 project_limit_reached`, `400` |
| `GET /api/v1/projects/{projectId}` | — | `200` project | `404` |
| `PATCH /api/v1/projects/{projectId}` | `{"name": "..."}` and/or `{"description": "..." \| null}`; `null` clears the description | `200` project; `updated_at` changes | `404`, `422` (also for an empty body), `400` |
| `DELETE /api/v1/projects/{projectId}` | — | `204`, no body | `404` |

Example validation error:

```json
{ "error": { "code": "validation_failed", "message": "Some fields are invalid.", "request_id": "…", "fields": { "name": "Enter a project name." } } }
```

**Limit.** A user may own at most `LIMIT_PROJECTS_PER_USER` projects (default
20). The check is serialised per user, so concurrent creates cannot exceed it.

**Deletion is permanent.** `DELETE` removes the project and everything scoped
to it (endpoints, events, replays, jobs as they are added) in one statement,
through database cascades. Deleting the account deletes all of the user's
projects the same way. There is no archive or restore.

### Endpoints

An endpoint is a secret receiving URL inside a project. Endpoint routes are
nested under a project the caller owns. Another user's project, an endpoint of
a different project, a missing endpoint, and a malformed id all return the same
`404 not_found`.

> Receiving webhooks at the endpoint URL (`/h/{token}`) is not available yet.
> It ships with the webhook ingestion change and will be documented here then.

An endpoint looks like this:

```json
{
  "id": "01936f3e-8c9d-7e1f-a2b3-c4d5e6f70819",
  "project_id": "01936f3e-7b8a-7c1d-9e2f-0a1b2c3d4e5f",
  "name": "Stripe staging",
  "enabled": true,
  "url": "http://localhost:5173/h/4kQ9…(43+ characters)",
  "last_received_at": null,
  "created_at": "2026-11-09T10:00:00Z",
  "updated_at": "2026-11-09T10:00:00Z"
}
```

| Field | Rules |
|---|---|
| `id` | UUIDv7, assigned by the server |
| `name` | Required. 1–100 characters after trimming whitespace; no control characters. Not unique |
| `enabled` | `true` on creation. A disabled endpoint keeps its URL and events |
| `url` | `INGEST_BASE_URL` + `/h/` + a token of at least 43 base62 characters (256 random bits). Treat it as a secret. `null` only if the stored token cannot be decrypted (for example, a lost `ENDPOINT_TOKEN_ENC_KEY`); rotating issues a new URL |
| `last_received_at` | Time of the most recent received request, or `null` |

The plaintext token is never stored: the database holds an HMAC lookup hash and
an AES-GCM encrypted copy, so the owner can see the URL again later. Endpoint
responses are `Cache-Control: no-store`.

| Method and path | Body | Success | Errors |
|---|---|---|---|
| `GET /api/v1/projects/{projectId}/endpoints` | — | `200 {"items":[endpoint, ...]}`, newest first. Bounded by the endpoint limit, so not paginated | `404` |
| `POST /api/v1/projects/{projectId}/endpoints` | `{"name": "..."}` | `201` endpoint, with `Location: /api/v1/projects/<projectId>/endpoints/<id>` | `404`, `422`, `409 endpoint_limit_reached`, `400` |
| `GET /api/v1/projects/{projectId}/endpoints/{endpointId}` | — | `200` endpoint | `404` |
| `PATCH /api/v1/projects/{projectId}/endpoints/{endpointId}` | `{"name": "..."}` and/or `{"enabled": true \| false}` | `200` endpoint. The URL does not change | `404`, `422` (also for an empty body or wrong types), `400` (unknown fields) |
| `POST /api/v1/projects/{projectId}/endpoints/{endpointId}/rotate` | — | `200` endpoint with a new `url`. The old URL stops working immediately; received events stay with the endpoint | `404` |
| `DELETE /api/v1/projects/{projectId}/endpoints/{endpointId}` | — | `204`, no body. Removes the endpoint and everything received through it | `404` |

Example:

```sh
curl -X POST "$APP_ORIGIN/api/v1/projects/$PROJECT_ID/endpoints" \
  -H "Origin: $APP_ORIGIN" -H 'Content-Type: application/json' \
  -b "$SESSION_COOKIE" -d '{"name":"Stripe staging"}'
```

**Limit.** A project may have at most `LIMIT_ENDPOINTS_PER_PROJECT` endpoints
(default 25). The check is serialised per project, so concurrent creates cannot
exceed it.

**Audit.** Create, rename, enable, disable, rotate, and delete each write an
`audit_log` entry with the actor, project, and endpoint id and name. Entries
never contain the token or URL. Request logs record the route pattern
(`/h/{token}`, or `unmatched`), never the raw path, so tokens do not reach logs.

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
