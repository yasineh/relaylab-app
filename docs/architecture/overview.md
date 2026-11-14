# Architecture overview

This page shows what is **implemented today**: three running processes
(`api`, `worker`, and the Better Auth `auth` service), two one-shot migration
steps, and PostgreSQL. The decisions behind it are recorded in
[`docs/decisions/`](../decisions/README.md); the deployment layout is in
[`docs/deployment.md`](../deployment.md).

## Components and trust boundaries

```mermaid
flowchart LR
  subgraph internet["Internet (untrusted)"]
    sender["Webhook senders<br/>(providers, curl)"]
    browser["Browser<br/>(RelayLab web app)"]
    dest["Replay destinations<br/>(public HTTPS only)"]
  end

  subgraph edge["Public edge: no session"]
    ingest["/h/{token}<br/>ingestion: token lookup, limits,<br/>credential redaction, store then ack"]
    static["Web app, /healthz, /readyz,<br/>GET /api/v1/config"]
  end

  subgraph authn["Authenticated zone: session cookie, same origin"]
    v1["/api/v1/*<br/>projects, endpoints, events, replays, audit log<br/>owner-scoped; origin and JSON checks"]
    sse["/api/v1/projects/{id}/events/stream<br/>SSE, session re-checked every 60 s"]
    proxy["/api/auth/* proxy<br/>+ shared secret, derived client IP"]
  end

  subgraph internal["Internal: never reachable from the internet"]
    auth["auth service<br/>Better Auth 1.7.18<br/>sessions, accounts, demo users"]
    worker["worker<br/>replay jobs (SKIP LOCKED leases),<br/>retention purge, guard heartbeat"]
    guard["outbound guard<br/>scheme, port, host, DNS and<br/>dial-time IP checks, no redirects"]
    pg[("PostgreSQL<br/>schema auth (Better Auth)<br/>schema app (goose)")]
  end

  sender -->|"any method"| ingest
  browser -->|"HTTPS"| static
  browser -->|"cookie"| v1
  browser -->|"cookie"| sse
  browser -->|"sign-in, sign-up, sign-out"| proxy
  proxy --> auth
  ingest -->|"insert + NOTIFY events (ids only)"| pg
  v1 -->|"app_runtime: owner-scoped queries;<br/>SELECT auth.session, auth.user"| pg
  pg -.->|"LISTEN events"| sse
  v1 -->|"replay + job + audit in one tx,<br/>NOTIFY replay_jobs"| pg
  pg -.->|"claim jobs"| worker
  worker -->|"URL replay"| guard
  guard -->|"only public addresses"| dest
  worker -->|"test-sink replay: stored as a new event,<br/>no network"| pg
  auth -->|"auth_runtime"| pg
```

All three processes run from two images: `relaylab` (Go: `api`, `api
migrate`, `worker`) and `relaylab-auth` (Node: the auth service and its
migrate command). Locally, Docker Compose also runs Mailpit (SMTP) and,
during development, the Vite dev server in front of the api.

## Inbound webhook flow

```mermaid
sequenceDiagram
  autonumber
  participant S as Sender
  participant I as api: /h/{token}
  participant P as PostgreSQL
  participant B as api: realtime broker
  participant W as Browser (event list)
  S->>I: POST /h/{token}/charges (headers, body)
  I->>I: rate limits (IP, endpoint), size and time limits
  I->>P: look up endpoint by token HMAC
  I->>I: replace credential header and query values with [REDACTED]
  I->>P: insert event, touch endpoint (one transaction)
  I-->>S: 200 with the event id
  I->>P: NOTIFY events (project id, event id)
  P-->>B: notification (one LISTEN connection per api instance)
  B->>P: load the summary, scoped to the stream's owner
  B-->>W: SSE event (a summary, never body or header values)
```

## Replay flow

```mermaid
sequenceDiagram
  autonumber
  participant U as Browser
  participant A as api: /api/v1
  participant P as PostgreSQL
  participant K as worker
  participant G as outbound guard
  participant D as Destination
  U->>A: POST .../replays (destination, Idempotency-Key)
  A->>A: owner check, URL pre-flight (no DNS), limits
  A->>P: replay + job + audit entry (one tx), NOTIFY replay_jobs
  A-->>U: 202 queued
  K->>P: claim job (SKIP LOCKED, per-project limit, lease)
  alt test sink
    K->>P: store the request as a new event on the sink endpoint
  else URL (REPLAY_ENABLED)
    K->>G: send request
    G->>G: resolve DNS, check every address, check the socket address
    G->>D: HTTPS request (no redirects, bounded time and size)
    D-->>G: response
    G-->>K: status, headers, capped body
  end
  K->>P: record the result, erase added header values, finish job
  U->>A: GET .../replays/{id}
  A-->>U: state, response, duration
```

## Request path through the API

Every request passes through the same middleware, in this order:

1. **Request ID**: reuse a safe `X-Request-ID` or generate one; attach a request-scoped logger.
2. **Real IP**: trust `X-Forwarded-For` only from `TRUSTED_PROXY_CIDRS`.
3. **Access log**: one JSON line per request with the route pattern, status, duration, and size.
4. **Recoverer**: panics become `500 internal_error`; the stack trace goes to logs only.
5. **Security headers**: `nosniff`, `Referrer-Policy`, `X-Frame-Options: DENY`, and a CSP without inline scripts.
6. **Header limit**: `431 headers_too_large` above `LIMIT_HTTP_MAX_HEADER_BYTES`.

`/api/*` additionally applies `LIMIT_API_MAX_BODY_BYTES`. Then:

- **`/api/auth/*`** is reverse-proxied to the auth service: hop-by-hop and client-supplied forwarding headers are removed, `X-Forwarded-For` is set to the derived client IP, the shared `X-RelayLab-Proxy` header is added, and the request is bounded by `LIMIT_AUTH_PROXY_TIMEOUT_SECONDS`.
- **`/api/v1/*`** applies, in order: the same-origin check on unsafe methods (`403 origin_rejected`), the JSON content-type check (`415`), and session validation (`401`/`403`) on every route except `GET /api/v1/config`.

Unknown `/api` and `/h` paths return the JSON error envelope. Other `GET` paths
fall back to the web app when it is embedded.

## Trust boundaries (current)

- **Authentication**: one origin for the SPA, the API, and authentication; no
  CORS. The Go API validates every `/api/v1` session itself (signature check,
  then a read-only lookup) and takes the acting user only from that session.
  The auth service is reachable only through the API proxy, which proves itself
  with a shared secret. Details: [Authentication and sessions](auth.md).
- **Database privileges**: the API and worker connect as `app_runtime`, which
  cannot create, alter, or drop objects, and can only read two `auth` tables.
  The auth service connects as `auth_runtime`. Only the migrate commands use
  `app_migrator` and `auth_migrator`.
- **Configuration**: secrets are typed so they cannot be printed by logs or
  error messages. The worker does not receive the session secret.
- **Public ingestion**: `/h/{token}` accepts untrusted requests without a
  session. The unguessable token is the only credential. Ingestion never reads
  the session cookie, can only insert events, and removes credential values
  before storing them. Details: [API reference](../api.md#webhook-ingestion-h).
- **Event explorer**: `/api/v1/projects/{id}/events` reads events only for the
  session user's projects, and the web app renders all received content as
  inert text. Details: [Received events](../security/events.md).
- **Live updates**: `/api/v1/projects/{id}/events/stream` requires the same
  session and ownership as the REST routes, re-validates both at least every
  60 s and at session expiry, and is capped per user. Database notifications
  carry ids only. Each stream loads summaries with an owner-scoped query, and
  summaries never include bodies, headers, or query values. Details:
  [API reference](../api.md#get-apiv1projectsprojectideventsstream).
- **Outbound requests (replay)**: the API validates and queues replays
  (`/api/v1/projects/{id}/events/{eventId}/replays`, owner-only; URL
  replays only behind `REPLAY_ENABLED` and a fresh worker heartbeat). The worker claims them from
  `app.jobs` and is the only process that sends user-directed requests,
  through `internal/replay/outbound`: HTTPS only, public addresses only
  (checked on every DNS answer and on the socket itself), no redirects,
  internal and RelayLab hosts blocked, and bounded time, size, and
  concurrency. Test-sink replays never open a socket: they are stored as new
  events through the ingestion insert path. Credential and signature headers
  are never forwarded; values users add are encrypted until delivery.
  Details: [Outbound requests](../security/outbound-replay.md).
- **Demo workspaces**: "Try the demo" signs in as a Better Auth anonymous
  user (`DEMO_ENABLED`). The Go API recognises demo users from the session,
  seeds each one a workspace on their first request, applies lower limits,
  and allows replay to the test sink only. The auth service deletes demo
  users after `DEMO_TTL`, and their data goes by cascade.
- **Retention**: one worker at a time (Postgres advisory lock) deletes events
  past `RETENTION_EVENT_TTL` or beyond the per-project cap, old audit entries,
  and finished jobs, in batches of at most 1,000 rows. The worker has no write
  access to the `auth` schema; the auth service prunes its own rate-limit
  rows. Details: [Data retention and deletion](../security/data-retention.md).

In production (prepared, not deployed: see [Deployment](../deployment.md)),
the auth service runs as a sidecar in the same Cloud Run service as the API,
so `AUTH_SERVICE_URL` is `http://localhost:3001` and the port is never
exposed. Request traffic may go through a transaction-mode pooler
(`DATABASE_URL`); `LISTEN` and the retention lock use a direct connection
(`DATABASE_DIRECT_URL`).
