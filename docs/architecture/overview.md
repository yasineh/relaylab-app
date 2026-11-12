# Architecture overview

This diagram shows what is **implemented today**. It grows as changes ship. The
target architecture and decisions are in [`docs/plan/mvp-plan.md`](../plan/mvp-plan.md).

```mermaid
flowchart LR
  browser["Browser<br/>(RelayLab SPA)"]
  sender["Webhook sender<br/>(provider or curl, untrusted)"]

  subgraph dev["Local development (Docker Compose)"]
    vite["web: Vite dev server<br/>(hot reload, proxies /api, /h, health)"]
    subgraph image["relaylab image (distroless, nonroot)"]
      api["api<br/>/healthz, /readyz<br/>/api/v1/me, projects, endpoints, events (session required)<br/>/api/v1/projects/{id}/events/stream (SSE, session required)<br/>/h/{token} public ingestion (no session)<br/>/api/auth/* proxy<br/>embedded web build<br/>realtime broker: one LISTEN connection per instance"]
      worker["worker<br/>/healthz, /readyz (outbound_guard), /metrics on :8081<br/>outbound guard for replay (no jobs yet)"]
      migrate["api migrate<br/>(one-shot, goose)"]
    end
    subgraph authimg["relaylab-auth image (Node 24)"]
      auth["auth: Better Auth 1.7.18<br/>:3001, internal network only"]
      authmigrate["auth migrate<br/>(one-shot, runs first)"]
    end
    pg[("PostgreSQL 17<br/>schema app: projects, endpoints, webhook_events,<br/>audit_log, goose_db_version<br/>schema auth: user, session, account,<br/>verification, rateLimit")]
    mailpit["Mailpit<br/>SMTP :1025, inbox :8025"]
  end

  sender -->|"POST /h/{token}: credential values redacted,<br/>stored before the ack"| api
  api -->|"after commit: pg_notify('events', ids only)"| pg
  pg -.->|"LISTEN events → broker → streams of that project"| api
  api -.->|"SSE: event summaries, owner-checked,<br/>session re-checked every ≤60 s"| browser
  browser -->|"HTTP, same origin, session cookie"| vite
  vite -->|"proxy"| api
  browser -.->|"production layout: api serves the SPA"| api
  api -->|"/api/auth/* + shared proxy header"| auth
  api -->|"app_runtime: DML on app,<br/>SELECT auth.session, auth.user"| pg
  worker -->|"app_runtime"| pg
  migrate -->|"app_migrator (DDL on app)"| pg
  auth -->|"auth_runtime (DML on auth)"| pg
  authmigrate -->|"auth_migrator (DDL on auth)"| pg
  auth -->|"verification and reset emails"| mailpit
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
  with a shared secret. Details: [Authentication and sessions](../security/authentication.md).
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
- **Outbound requests**: the worker holds the only client allowed to send
  user-directed requests (`internal/replay/outbound`). HTTPS only, public
  addresses only (checked on every DNS answer and on the socket itself), no
  redirects, internal and RelayLab hosts blocked, and bounded time, size, and
  concurrency. Nothing uses it yet; replay jobs arrive with `add-event-replay`.
  Details: [Outbound requests](../security/outbound-replay.md).

In production (prepared by `add-production-readiness`), the auth service runs
as a sidecar in the same Cloud Run service as the API, so `AUTH_SERVICE_URL`
is `http://localhost:3001` and the port is never exposed.
