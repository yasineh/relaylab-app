# D7. Server-Sent Events fed by LISTEN/NOTIFY

Status: Accepted

## Context

New webhooks should appear in the browser without a reload, and missed events must load after a reconnect.

## Decision

Use SSE with `Last-Event-ID` resume. Ingestion sends `NOTIFY` with ids only after commit; one `LISTEN` connection per API instance fans out to that project's streams, which load summaries with an owner-scoped query. No polling.

## Consequences

- Live updates without WebSockets or a broker.
- `LISTEN` needs a direct database connection (`DATABASE_DIRECT_URL`) when requests go through a transaction pooler.

## See also

- [Live event stream](../api.md#get-apiv1projectsprojectideventsstream)
