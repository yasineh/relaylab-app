# Data retention and deletion

RelayLab keeps received webhooks only for a limited time. Webhook payloads
often contain confidential or personal data, such as customer details or
tokens a provider includes. This page describes what is stored, how long it
is kept, and how it is deleted.

The web app states the retention period in the event explorer and the project
overview. The project overview and each endpoint page also show a notice that
payloads may contain confidential or personal data.

## What is stored

| Data | Where | Contains |
|---|---|---|
| Received webhooks | `app.webhook_events` | Method, path, query, headers (credential values replaced with `[REDACTED]` before storage), the body as received, content type, a truncated source IP prefix (IPv4 /24, IPv6 /48), user agent, receive time |
| Replays | `app.replay_attempts` | Destination, method, header names you added, the response status, headers, and a bounded body excerpt. Values of headers you added are stored encrypted and erased when the replay finishes |
| Audit log | `app.audit_log` | Who did what and when (for example, created a replay or rotated an endpoint URL). No payloads, tokens, or header values |
| Background jobs | `app.jobs` | The replay id to deliver, state, and attempt counts |
| Rate-limit counters | `auth."rateLimit"` | Client IP and route, request count, last request time. The only stored full IP addresses |

Accounts, projects, and endpoints are kept until you delete them.

## Automatic deletion

The worker runs a purge every `RETENTION_INTERVAL`. Times are measured with
the database clock.

| Setting | Default | What it deletes |
|---|---|---|
| `RETENTION_EVENT_TTL` | 168h (7 days) | Events received longer ago than this, with their replays |
| `RETENTION_MAX_EVENTS_PER_PROJECT` | 10000 | The oldest events of a project beyond this many, with their replays |
| `RETENTION_AUDIT_TTL` | 2160h (90 days) | Audit log entries older than this |
| `RETENTION_JOB_TTL` | 168h (7 days) | Finished (`done` or `dead`) background jobs not updated for this long |
| `RETENTION_INTERVAL` | 10m | Time between purge runs |

Durations use Go syntax (`h`, `m`, `s`; there is no day unit). A public demo
deployment sets `RETENTION_EVENT_TTL=24h`. `RETENTION_EVENT_TTL=0` turns off
deletion by age for local debugging; the worker and API refuse to start with
it in production.

**Timing.** An expired event disappears from the event list, its detail page,
and live-stream resume within one interval plus one purge run, so within
about 10 minutes of expiring with the defaults.

**The per-project cap.** On each run, projects holding more than the cap lose
their oldest events (by receive time, then id) until exactly the cap remains.
Between runs a busy project can briefly hold more than the cap. Ingestion rate
limits bound how far: with the defaults (60 requests per minute and a burst of
30 per endpoint, per API instance), one endpoint can add at most about 630
events in one 10-minute interval.

**One purge at a time.** A Postgres advisory lock lets only one worker purge;
others skip that run. Each statement deletes at most 1,000 rows, so ingestion
and dashboard queries are not blocked, and one run stops after 2 minutes and
continues on the next run. Each batch commits on its own, so a failed run
keeps the progress it made.

**Pending work is never purged by age.** Queued and running jobs stay however
old they are. Running jobs finish normally; a job whose replay was deleted
finishes without writing anything.

**Safety net for orphaned jobs.** Deleting a project deletes its jobs in the
same transaction. As a backstop, each run also deletes any job that is not
running and whose project or replay no longer exists. The log reports these
as `jobs_orphaned`, which is normally 0.

**Observability.** Each run logs one line, `retention purge finished`, with
counts per table (`events`, `replay_attempts`, `audit_log`, `jobs`,
`jobs_orphaned`) and the duration. It never logs payloads or ids. Metrics on
the worker's `/metrics`: `retention_deleted_total{table}` and
`retention_run_duration_seconds`.

**Rate-limit rows.** The auth service, which owns the `auth` schema, deletes
`auth."rateLimit"` rows whose last request is older than the longest
rate-limit window (one hour), every 10 minutes. Rows still inside a window are
kept, because they are what enforces the limit.

**Demo workspaces.** The auth service deletes demo users older than
`DEMO_TTL` (24 hours by default) every 10 minutes, and with them, by
cascade, everything they own: projects, endpoints, events, replays, jobs,
audit entries, and sessions. Their sessions stop working at expiry, before
the purge runs. Demo projects also keep at most `DEMO_MAX_EVENTS` (200)
events: the retention purge deletes the oldest beyond that on each run.

**Not handled here.** The Go worker has no write access to the `auth` schema.
Better Auth deletes expired sessions itself.

## Deleting data yourself

All deletions are permanent. There is no undo and no soft delete.

| Action | Where | Deletes |
|---|---|---|
| Delete event | Event detail, **Delete event** (confirmation dialog) | The event and its replays |
| Clear events | Endpoint detail, **Clear events** (type the endpoint name to confirm) | Every event the endpoint received, and their replays. The endpoint and its URL stay |
| Delete endpoint | Endpoint detail | The endpoint and all its events and replays |
| Delete project | Project settings | The project, its endpoints, events, replays, jobs, and audit log |
| Delete account | Account settings | The account and all its projects |

The API routes are in [the API reference](../api.md#events).

## What deletion removes

These rules hold for every way an event is deleted: by age, by the cap, by
you, or with its endpoint or project.

- **Replays go with their event.** Deleting an event deletes its replay
  attempts, including any encrypted header values not yet erased (database
  cascade).
- **Test-sink events stay.** A replay to a test sink created a new event on
  another endpoint. That event has its own retention; when the replay is
  deleted, the new event only loses its link to it.
- **Deleting a sink endpoint keeps the replays** that delivered to it and
  clears only their sink reference.
- **Deleting an account deletes its projects**, and with them everything
  above.

The ERD in [docs/architecture/erd.md](../architecture/erd.md) lists every
foreign key and its delete rule.
