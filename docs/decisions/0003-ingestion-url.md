# D3. Ingestion on the app host under /h/{token}

Status: Accepted

## Context

Webhook senders need a URL. A separate ingestion domain would isolate cookies, but no custom domain exists yet.

## Decision

Receive webhooks at `https://<app-host>/h/{token}[/{subpath}]`. `INGEST_BASE_URL` stays configurable so a separate subdomain can be added with a custom domain. Ingestion never reads cookies and redacts the `Cookie` header before storage.

## Consequences

- One host to deploy and secure.
- The browser's session cookie can reach `/h/*`; ingestion ignores it and stores it only as `[REDACTED]`.

## See also

- [Webhook ingestion](../api.md#webhook-ingestion-h)
