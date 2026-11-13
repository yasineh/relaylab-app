# API reference

The HTTP API that exists **today**. Later changes document their routes here when they ship.

Everything is served from the application origin (`APP_ORIGIN`): the web app,
the dashboard API under `/api/v1`, and authentication under `/api/auth`. The
API sends no CORS headers, so it can only be used from the RelayLab web app or
from clients that are not browsers.

## Conventions

- **Format**: JSON request and response bodies. Requests with a body to `/api/v1` must use `Content-Type: application/json`.
- **Authentication**: the session cookie set by `/api/auth`. See [Authentication and sessions](security/authentication.md).
- **CSRF**: `POST`, `PUT`, `PATCH`, and `DELETE` on `/api/v1` require `Origin: <APP_ORIGIN>`. When the browser sends no `Origin`, `Sec-Fetch-Site: same-origin` is required instead.
- **Request IDs**: every response has `X-Request-ID`. A safe inbound value (1–64 characters of `[A-Za-z0-9._-]`) is reused.
- **Limits**: request headers up to `LIMIT_HTTP_MAX_HEADER_BYTES` (32 KiB); `/api` bodies up to `LIMIT_API_MAX_BODY_BYTES` (64 KiB); proxied `/api/auth` requests are bounded by `LIMIT_AUTH_PROXY_TIMEOUT_SECONDS` (10 s); `LIMIT_PROJECTS_PER_USER` projects per user (20); `LIMIT_ENDPOINTS_PER_PROJECT` endpoints per project (25); list pages of `LIMIT_PAGE_DEFAULT` items (50) and at most `LIMIT_PAGE_MAX` (100); `LIMIT_STREAMS_PER_USER` open live event streams per user, per API instance (5).

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
| 400 | `invalid_cursor` | A page cursor is malformed, or was issued for different filters or sort order |
| 404 | `not_found` | Unknown path (on `/api/v1`, only after authentication succeeds), or a resource that is missing, owned by someone else, or has a malformed id. These cases are indistinguishable |
| 405 | `method_not_allowed` | Known path, wrong method |
| 413 | `body_too_large` | Body over the limit |
| 409 | `project_limit_reached` | Creating a project beyond `LIMIT_PROJECTS_PER_USER` |
| 409 | `endpoint_limit_reached` | Creating an endpoint beyond `LIMIT_ENDPOINTS_PER_PROJECT` |
| 415 | `unsupported_media_type` | Body is not `application/json` |
| 429 | `too_many_streams` | Opening a live event stream beyond `LIMIT_STREAMS_PER_USER` |
| 409 | `idempotency_conflict` | An `Idempotency-Key` reused for a different replay request |
| 422 | `invalid_destination` | A replay destination that is not allowed (`error.fields` says why) |
| 429 | `replay_queue_full` | The project already has `REPLAY_PROJECT_QUEUE_DEPTH` replays waiting or running |
| 429 | `rate_limited` | Replay creations beyond `REPLAY_USER_RATE_PER_MINUTE`; `Retry-After` says when to retry |
| 503 | `replay_unavailable` | Replay to URLs is turned off, or no worker has reported an active outbound guard in the last 60 s. Test-sink replays are never refused this way |
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
{
  "signup_enabled": true,
  "email_verification_required": true,
  "replay_enabled": true,
  "replay_url_available": true,
  "retention": { "event_ttl_seconds": 604800, "max_events_per_project": 10000 }
}
```

| Field | Meaning |
|---|---|
| `signup_enabled` | Whether public sign-up is open (`AUTH_SIGNUP_ENABLED`) |
| `email_verification_required` | Whether users must verify their email before signing in (`AUTH_REQUIRE_EMAIL_VERIFICATION`) |
| `replay_enabled`, `replay_url_available` | Replay availability; see [Replays](#replays) |
| `retention.event_ttl_seconds` | How long events are kept (`RETENTION_EVENT_TTL`). `0` means events are not deleted by age, which only development allows |
| `retention.max_events_per_project` | Events kept per project before the oldest are deleted (`RETENTION_MAX_EVENTS_PER_PROJECT`) |

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

Requests sent to the endpoint URL are received as described in
[Webhook ingestion](#webhook-ingestion-h).

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

### Events

Received webhooks of a project. They can be read and deleted, never changed. Every route requires a session and
only returns events of projects the caller owns. Another user's project or
event, a project or event that does not exist, and a malformed id all give the
same `404 not_found`. Responses are `Cache-Control: no-store`.

#### `GET /api/v1/projects/{projectId}/events`

One page of events, newest first unless `sort=asc`. Items never include the
body, headers, or query.

| Parameter | Value |
|---|---|
| `endpoint_id` | Only this endpoint's events. An endpoint of another project matches nothing |
| `method` | One or more of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS`, comma-separated or repeated; case-insensitive |
| `received_after` | RFC 3339 time, inclusive |
| `received_before` | RFC 3339 time, exclusive; must be later than `received_after` |
| `content_type` | Case-insensitive prefix of the content type, at most 100 characters; `%` and `_` match literally |
| `sort` | `desc` (default) or `asc` |
| `limit` | 1 to `LIMIT_PAGE_MAX` (100); default `LIMIT_PAGE_DEFAULT` (50) |
| `cursor` | The previous response's `next_cursor`, with the same filters and sort |

Each parameter except `method` may be given once. Invalid values give
`422 validation_failed` naming the parameter in `error.fields`. Unknown
parameters are ignored.

```sh
curl "$APP_ORIGIN/api/v1/projects/$PROJECT_ID/events?method=POST&limit=2" -b "$SESSION_COOKIE"
```

```json
{
  "items": [
    {
      "id": "019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b",
      "endpoint_id": "019a6b1d-0000-7000-8000-000000000001",
      "endpoint_name": "Stripe staging",
      "method": "POST",
      "path": "/orders",
      "content_type": "application/json",
      "body_size": 20,
      "received_at": "2026-11-09T11:36:12.481Z"
    }
  ],
  "next_cursor": "eyJpZCI6IjAxYTFjZjQ3LTdlNTEtNzBjOS05NzMxLTY5OGRkOTcxNjBiYyIsImRpciI6ImRlc2MiLCJmIjoiMDdjYmNkYzVkNjRiMGNkMiJ9"
}
```

`next_cursor` is `null` on the last page, and an owned project with no
matching events returns `{"items":[],"next_cursor":null}`. Pages are keyed by
event id, not offset, so events arriving while you page cause no duplicates or
gaps in the range you are reading: newer events appear when you start from the
first page again. Cursors are opaque and are not secrets. One can only position
a query inside the projects the caller already owns, and using it with
different filters or sort gives `400 invalid_cursor`.

#### `GET /api/v1/projects/{projectId}/events/{eventId}`

The full event. Query parameters, including `reveal`, are ignored: original
credential values are not stored, so there is nothing to reveal.

```json
{
  "id": "019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b",
  "endpoint_id": "019a6b1d-0000-7000-8000-000000000001",
  "endpoint_name": "Stripe staging",
  "method": "POST",
  "path": "/orders",
  "content_type": "application/json",
  "body_size": 20,
  "received_at": "2026-11-09T11:36:12.481Z",
  "raw_query": "token=%5BREDACTED%5D&page=2",
  "query": [
    { "name": "page", "value": "2", "redacted": false },
    { "name": "token", "value": "[REDACTED]", "redacted": true }
  ],
  "headers": [
    { "name": "Authorization", "value": "[REDACTED]", "redacted": true },
    { "name": "Content-Type", "value": "application/json", "redacted": false }
  ],
  "redacted_count": 2,
  "body": { "encoding": "utf8", "text": "{\"hello\":\"relaylab\"}", "is_json": true, "json_invalid": false, "size": 20 },
  "source_ip_prefix": "203.0.113.0/24",
  "user_agent": "curl/8.7.1",
  "correlation_id": "98cd9eb0-2f07-4c93-8f28-4513d9576552"
}
```

- `headers` keep their stored order (sorted by name; repeated headers in
  arrival order). `query` is sorted by name, each name's values in arrival
  order; `raw_query` is exactly as received (after redaction).
- `redacted: true` means the value was removed when the request arrived. It is
  set only when the stored value is `[REDACTED]` **and** the name matches a
  redaction rule, so a sender that literally sent `[REDACTED]` in an ordinary
  header is not mislabelled.
- `redacted_count` is the number of header values removed at receipt plus the
  query values marked as removed.
- `body.encoding` is `utf8` (the body is in `text`) or `binary` (base64 in
  `base64`). `is_json` means the body is valid JSON with a JSON content type;
  `json_invalid` means a JSON content type with an invalid body. Clients parse
  `text` themselves, which keeps key order and duplicate keys exactly as sent.
- `source_ip_prefix` and `user_agent` are `null` when unknown.

#### `GET /api/v1/projects/{projectId}/events/{eventId}/body`

The exact body bytes as a download: `Content-Type: application/octet-stream`,
`Content-Disposition: attachment; filename="event-{id}.bin"`,
`X-Content-Type-Options: nosniff`, and `Content-Security-Policy: sandbox`, so
a browser never renders it, even when the sender declared `text/html`.

#### `DELETE /api/v1/projects/{projectId}/events/{eventId}`

Permanently deletes the event and its replay attempts. There is no undo.
Returns `204` with no body. Events the caller can't see, including an event
under a different project id, give `404 not_found`. Events that a replay
delivered to a test sink are separate events and stay; their link to the
deleted replay is cleared.

#### `DELETE /api/v1/projects/{projectId}/endpoints/{endpointId}/events`

Permanently deletes every event the endpoint received, with their replay
attempts. The endpoint and its URL stay. Returns `200`:

```json
{ "deleted": 120 }
```

The server deletes in batches of 1,000 for up to 30 seconds per request. If
events remain, `deleted` is what went so far: repeat the request until it
returns `{"deleted": 0}` (the web app does this). An endpoint the caller can't
see gives `404 not_found`.

```sh
curl -X DELETE "$APP_ORIGIN/api/v1/projects/$PROJECT_ID/endpoints/$ENDPOINT_ID/events" \
  -b "$SESSION_COOKIE" -H "Origin: $APP_ORIGIN"
```

Events are also deleted automatically after the retention period; see
[Data retention](security/data-retention.md).

#### `GET /api/v1/projects/{projectId}/events/stream`

New events of the project as they are received, as
[Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html)
(`Content-Type: text/event-stream`, `Cache-Control: no-store`,
`X-Accel-Buffering: no`). The web app opens it with `EventSource`.

Before the stream opens, the usual checks apply and answer with the error
envelope: `401 unauthenticated` or `401 session_expired`, `404 not_found` for a
project that is not the caller's, and `429 too_many_streams` when the user
already has `LIMIT_STREAMS_PER_USER` streams open on this API instance. A slot is
freed as soon as a stream closes.

Messages:

```text
retry: 3000

id: 019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b
event: event.created
data: {"id":"019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b","endpoint_id":"019a6b1d-0000-7000-8000-000000000001","endpoint_name":"Stripe staging","method":"POST","path":"/orders","content_type":"application/json","body_size":20,"received_at":"2026-11-09T11:36:12.481Z"}

: ping

event: resync.required
data: {"reason":"too_many_missed"}
```

| Message | Meaning |
|---|---|
| `retry: 3000` | Sent first. The reconnect delay for clients that use EventSource's built-in retry |
| `event.created` | One new event. `id` is the event id; `data` is the same summary as a list item, on one line. It never contains the body, headers, or query |
| `: ping` | A comment every 20 seconds, so proxies keep idle streams open |
| `resync.required` | The stream ends and the client should reload its list, then open a new stream without a resume point. `reason` is `too_many_missed`, `slow_client` (the client fell more than 256 events behind), `listener_restart` (the server's database listener reconnected, so notifications may have been lost), or `project_not_found` (the project was deleted) |
| `session.expired` | The session expired, was signed out, or was revoked. The stream ends; sign in again |

Received content can't break the framing. `data` is JSON on a single line, so
newlines and other control characters in paths or names are escaped.

**Resume.** To continue after a disconnect, reconnect with the last received
id in the `Last-Event-ID` header (EventSource does this itself) or the
`last_event_id` query parameter; the header wins when both are present. The
server first replays the project's events received after that id, oldest
first, then continues live. It also re-reads events received up to 5 seconds
before that id, because an event can commit slightly after one received later.
Clients should ignore ids they already have. One connection never sends the
same event twice. If more than 500 events would be replayed, the server sends
`resync.required` instead. An id that is malformed, unknown, or belongs to
another project is ignored: no replay, live events only.

**Lifetime.** The server re-checks the session and project ownership at least
every `STREAM_REVALIDATE_INTERVAL` (default and maximum 60 s), and also when
the session's expiry time is reached. A session that Better Auth extended in
the meantime keeps the stream open. Streams also end when the API instance
shuts down; clients reconnect and resume. Any hosting proxy must allow
long-lived responses: on Cloud Run, set the request timeout to 3600 s (the
maximum). Streams are closed at that point and clients resume transparently.

```sh
curl -N "$APP_ORIGIN/api/v1/projects/$PROJECT_ID/events/stream" -b "$SESSION_COOKIE" \
  -H "Last-Event-ID: 019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b"
```

### Replays

Re-send a stored event to a URL you control (through the
[outbound guard](security/outbound-replay.md)) or to a **test sink**: another
enabled endpoint of the same project, where the replay is stored as a new event
without any network request. Replays run in the background; the original event
is never changed. Every route requires a session and only works on the
caller's projects (`404 not_found` otherwise); unsafe methods need the usual
same-origin and JSON checks. Responses are `Cache-Control: no-store`.

`GET /api/v1/config` reports `replay_enabled` (`REPLAY_ENABLED`, which turns
on replay to URLs) and `replay_url_available` (also a worker reported an active
outbound guard in the last 60 s). Replays to a test sink are always available:
they never leave RelayLab.

#### Request body (preview and create)

```json
{
  "destination": { "type": "url", "url": "https://hooks.example.com/receive" },
  "method": "POST",
  "header_overrides": { "Authorization": "Bearer …" }
}
```

| Field | Rules |
|---|---|
| `destination` | `{"type":"url","url":"https://…"}` or `{"type":"sink","endpoint_id":"…"}`. URLs are checked without DNS when the request is made (scheme, credentials, host, port, internal and RelayLab hosts; `422 invalid_destination`) and fully when sent. A sink must be an enabled endpoint of the same project (`404` for any other) |
| `method` | `GET`, `POST`, `PUT`, `PATCH`, or `DELETE`; default the original method |
| `header_overrides` | Optional object, at most 20 headers. Names are HTTP tokens; values at most 4 KiB; no CR, LF, or NUL. `Cookie`, `Host`, `Proxy-Authorization`, hop-by-hop, forwarding headers, and `X-RelayLab-Replay-Id` cannot be set (`422 validation_failed`). Values are stored encrypted, erased when the replay finishes, and always returned as `[REDACTED]` |

The request sent is the original body byte for byte, the original
`Content-Type`, the chosen method, and the URL exactly as entered (the original
path and query are not added). Original headers are forwarded except
credentials (the central redaction set), signature headers (any name containing
`signature` or `hmac`), values removed at receipt, hop-by-hop and forwarding
headers. Overrides are applied last. Every delivery carries
`X-RelayLab-Replay-Id: <replay id>`; destinations should de-duplicate on it,
because delivery is at least once.

#### `POST /api/v1/projects/{projectId}/events/{eventId}/replays/preview`

Validates the body and returns what would be sent; nothing is stored or sent.
Errors are the same as for creation.

```json
{
  "method": "POST",
  "target": "https://hooks.example.com/receive",
  "destination": { "type": "url", "url": "https://hooks.example.com/receive" },
  "headers": [
    { "name": "Content-Type", "value": "application/json", "redacted": false },
    { "name": "Authorization", "value": "[REDACTED]", "redacted": true },
    { "name": "X-Relaylab-Replay-Id", "value": "(assigned when the replay is created)", "redacted": false }
  ],
  "stripped_headers": ["Stripe-Signature", "Cookie"],
  "body_size": 20,
  "content_type": "application/json"
}
```

#### `POST /api/v1/projects/{projectId}/events/{eventId}/replays`

Queues a replay: `202` with the replay in state `queued`. With an
`Idempotency-Key` header (at most 128 characters), repeating the same request
within 24 hours returns the original replay with `200`; the same key with a
different request is `409 idempotency_conflict`.

```sh
curl -X POST "$APP_ORIGIN/api/v1/projects/$PROJECT_ID/events/$EVENT_ID/replays" -b "$SESSION_COOKIE" \
  -H "Origin: $APP_ORIGIN" -H "Content-Type: application/json" -H "Idempotency-Key: $(uuidgen)" \
  -d '{"destination":{"type":"sink","endpoint_id":"'"$SINK_ID"'"}}'
```

#### Replay objects

```json
{
  "id": "019a6b2f-…",
  "event_id": "019a6b1e-…",
  "state": "succeeded",
  "outcome": "succeeded",
  "failure_category": null,
  "failure_message": null,
  "destination": { "type": "sink", "endpoint_id": "019a6b1d-…", "endpoint_name": "Sink" },
  "target": "Test sink: Sink",
  "method": "POST",
  "tries": 1,
  "response_status": 200,
  "duration_ms": 12,
  "created_at": "2026-11-13T09:20:00.123Z",
  "finished_at": "2026-11-13T09:20:00.140Z"
}
```

`state` is `queued`, `running`, `succeeded`, or `failed`. Any HTTP response,
including 3xx, 4xx, and 5xx, is `succeeded`; `failed` carries a
`failure_category` (an [outbound guard category](security/outbound-replay.md#outcomes-and-what-users-see),
`sink_disabled`, `worker_lost`, or `override_unavailable`) and a safe
`failure_message`. The detail adds `started_at`, `header_overrides` (names, values
`[REDACTED]`), and `response`: `{status, headers, body}` where sensitive header
values are `[REDACTED]` and `body` is an excerpt (at most
`REPLAY_RESPONSE_EXCERPT_BYTES`, 64 KiB) as `text` (UTF-8) or `base64`, with
`truncated`.

#### `GET /api/v1/projects/{projectId}/replays` and `GET …/events/{eventId}/replays`

Newest first. Parameters: `event_id` (project list only), `state`, `outcome`
(`succeeded` or a failure category), `limit` (1–`LIMIT_PAGE_MAX`, default
`REPLAY_HISTORY_PAGE_DEFAULT` 25), `cursor` (the previous `next_cursor`).
Response: `{"items": [...], "next_cursor": "…" | null}`.

#### `GET /api/v1/projects/{projectId}/replays/{replayId}`

One replay with its detail.

### Audit log

#### `GET /api/v1/projects/{projectId}/audit-log`

The project's recorded actions, newest first, with `limit` and `cursor` like the
replay list. Read-only: there is no route that changes or deletes entries.

```json
{
  "items": [
    {
      "id": "019a6b2f-…",
      "action": "replay.created",
      "actor_user_id": "u_…",
      "actor_name": "Ada Lovelace",
      "resource_type": "replay",
      "resource_id": "019a6b2f-…",
      "metadata": { "event_id": "019a6b1e-…", "destination_type": "url", "destination_host": "hooks.example.com", "method": "POST" },
      "created_at": "2026-11-13T09:20:00.123Z"
    }
  ],
  "next_cursor": null
}
```

Metadata never contains URL paths, query strings, credentials, header values,
bodies, or IP addresses.

## Webhook ingestion (`/h/*`)

The public receiving URL of an endpoint is `{INGEST_BASE_URL}/h/{token}`
(`INGEST_BASE_URL` defaults to `APP_ORIGIN`). It needs no authentication: the
unguessable token is the credential. Anything after the token is kept as the
event's path, so `/h/{token}/orders/42?x=1` is stored with path `/orders/42`.

Ingestion never reads the dashboard session cookie, sets no cookies or CORS
headers, and cannot read or change stored events.

```sh
curl -X POST "$ENDPOINT_URL" -H 'Content-Type: application/json' -d '{"hello":"relaylab"}'
```

```json
{ "ok": true, "event_id": "019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b" }
```

The acknowledgement is sent only after the event is committed to the database
(`Cache-Control: no-store`, with `X-Request-ID`). `HEAD` gets the same status
with no body.

**What is stored.** Method, path after the token, raw query string and parsed
parameters, every header (sorted by name; repeated headers keep their order),
content type, the body bytes exactly as received, body size, receive time
(UTC), the client address truncated to IPv4 /24 or IPv6 /48, the user agent,
and the request id. A JSON body with a JSON content type is also stored parsed.
Any content type is accepted; other bodies are stored as raw bytes and marked
`utf8` or `binary`.

**Redaction on arrival.** Before an event is stored, logged, or published,
the values of credential headers and query parameters are replaced with
`[REDACTED]`; the names stay visible. Built in: the headers `Authorization`,
`Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`, `Api-Key`,
`X-Auth-Token`, `X-Access-Token`, `X-CSRF-Token`, `X-Amz-Security-Token`, and
`X-Goog-Api-Key`; the query parameters `key`, `code`, `sig`, `auth`, and
`signature`; and any header or parameter whose name contains `token`,
`secret`, `password`, `passwd`, `api-key`, `apikey`, `api_key`, `signature`,
`credential`, `session`, or `hmac` (this covers provider signature headers such
as `Stripe-Signature`, `X-Hub-Signature-256`, and `X-Shopify-Hmac-Sha256`).
`INGEST_REDACT_EXTRA_HEADERS` and `INGEST_REDACT_EXTRA_PARAMS` add names; they
cannot remove built-in ones. Request **bodies are not redacted**: they are
stored exactly as sent, so they may contain confidential or personal data.

| Method | Supported |
|---|---|
| `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS` | Yes |
| Anything else (`TRACE`, `CONNECT`, …) | No: `405` with `Allow` |

| Status | Code | When |
|---|---|---|
| 200 | — | Stored; body `{"ok":true,"event_id":"…"}` |
| 400 | `invalid_request` | The body could not be read (for example, the client disconnected) |
| 404 | `endpoint_not_found` | Unknown, rotated-away, deleted, or malformed token. Reveals nothing about projects or users |
| 405 | `method_not_allowed` | Unsupported method |
| 408 | `request_timeout` | The body did not arrive within `LIMIT_INGEST_BODY_TIMEOUT_SECONDS` (10 s) |
| 410 | `endpoint_disabled` | The endpoint is disabled |
| 413 | `payload_too_large` | Body over `LIMIT_INGEST_MAX_BODY_BYTES` (256 KiB; at most 1 MiB). A body of exactly the limit is accepted |
| 414 | `uri_too_long` | Path after the token over 2048 characters, or query string over 8192 |
| 429 | `rate_limited` | Rate limit exceeded; see `Retry-After` (seconds) |
| 431 | `headers_too_large` | Headers over `LIMIT_HTTP_MAX_HEADER_BYTES` (32 KiB) or more than `LIMIT_INGEST_MAX_HEADER_FIELDS` (100) fields |
| 503 | `unavailable` | The event could not be stored; nothing was acknowledged. Safe to retry |

Rejected requests are never stored. Headers must arrive within 5 seconds.

**Rate limits** (per API instance, so the effective limit is roughly instances
× limit): `LIMIT_INGEST_RATE_PER_ENDPOINT_PER_MINUTE` (60, burst
`LIMIT_INGEST_RATE_ENDPOINT_BURST` 30) per endpoint, and
`LIMIT_INGEST_RATE_PER_IP_PER_MINUTE` (120, burst `LIMIT_INGEST_RATE_IP_BURST`
60) per client IP. The per-IP limit also counts requests to unknown tokens.
Forwarding headers such as `X-Forwarded-For` are used for the client address
only when the direct peer is in `TRUSTED_PROXY_CIDRS`.

**Metrics.** With `METRICS_ADDR` set, `GET /metrics` on that separate listener
serves `ingest_requests_total{outcome}`, `ingest_duration_seconds`, and
`ingest_body_bytes`. Outcomes: `accepted`, `rejected_size`, `rejected_rate`,
`rejected_method`, `rejected_timeout`, `rejected_invalid`, `not_found`,
`disabled`, `error`. `/metrics` is never served on the public port (404).

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
