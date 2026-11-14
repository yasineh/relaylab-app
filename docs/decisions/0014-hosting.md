# D14. Deploy-ready for Cloud Run with Neon; not deployed yet

Status: Accepted

## Context

The product must be production-ready without a live deployment being decided yet.

## Decision

Prepare Google Cloud Run: one app service (Go API with the auth service as a localhost sidecar, on the default `*.run.app` domain), a worker service, and migration jobs; Neon Postgres with no provider-specific features so Cloud SQL is a drop-in; secrets in Secret Manager; built-in monitoring plus structured logs and Error Reporting, with a configurable alert channel.

## Consequences

- Everything is verified locally (manifests, images, a production-mode stack); the first deployment follows the [deployment runbook](../deployment.md).
- Implementation: two migration jobs (auth, then app), because they need different images.

## See also

- [Deployment](../deployment.md)
