# RelayLab documentation

## Using RelayLab

- [Getting started](guide/getting-started.md): open RelayLab, create a project and an endpoint, send a test webhook with curl.
- [Inspecting and replaying webhooks](guide/webhook-workflow.md): how requests are received, reading headers, query, and body, replaying, and reading the result.
- [Product demo walkthrough](demo/walkthrough.md): the recorded demo, step by step, with the synthetic webhook and how to record it again.

## Reference

- [API reference](api.md): every dashboard route, the ingestion contract, the error envelope, status codes, and limits.
- [Security and privacy](security.md): what is stored, credential removal, secrets, retention, replay restrictions, and the limits table.
  - [Received events](security/events.md)
  - [Outbound requests and SSRF protections](security/outbound-replay.md)
  - [Data retention and deletion](security/data-retention.md)

## Architecture

- [Architecture overview](architecture/overview.md): components, trust boundaries, and the inbound and replay flows.
- [Authentication](architecture/auth.md): Better Auth, session validation in Go, cookies, CSRF and origin rules, demo users, and database roles.
- [Database schema](architecture/erd.md): the ERD, every table, and a generated listing checked against the migrations.
- [Decision records](decisions/README.md): decisions D1–D20 and how they were amended.

## Developing and operating

- [Development guide](development/README.md): prerequisites, setup, configuration, migrations, running everything, and Docker.
- [Testing](development/testing.md): unit, integration, and end-to-end suites.
- [Continuous integration](ci.md)
- [Deployment](deployment.md): Cloud Run and Neon or Cloud SQL, secrets, migrations, the deploy workflow, rollback, and monitoring. Not deployed yet.

## Background

- [MVP plan](plan/mvp-plan.md): the original plan and decisions table.
- [Brand and design system](design/design-system.md)
