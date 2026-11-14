# Decision records

Architecture and product decisions D1–D20 from the [MVP plan](../plan/mvp-plan.md), one record each: the context, the decision, its consequences, and its status. Later changes that amended a decision are noted in its record.

| Record | Decision | Status |
|---|---|---|
| [D1](0001-identity.md) | Better Auth for identity, sessions validated by Go through the database | Accepted |
| [D2](0002-csrf-origin.md) | Same-origin deployment instead of CSRF tokens and CORS | Accepted |
| [D3](0003-ingestion-url.md) | Ingestion on the app host under /h/{token} | Accepted |
| [D4](0004-endpoint-token.md) | Endpoint tokens stored as a lookup hash and an encrypted copy | Accepted |
| [D5](0005-job-queue.md) | A Postgres job queue, no Redis | Accepted |
| [D6](0006-rate-limiting.md) | In-process rate limiting, approximate across instances | Accepted |
| [D7](0007-realtime.md) | Server-Sent Events fed by LISTEN/NOTIFY | Accepted |
| [D8](0008-ids.md) | UUIDv7 for app tables; text for Better Auth ids | Accepted |
| [D9](0009-payload-storage.md) | Store bodies exactly; redact credential values on arrival | Accepted |
| [D10](0010-retention.md) | Time-based retention with a per-project cap | Accepted |
| [D11](0011-replay-safety.md) | Replay behind an SSRF guard, URL replay off by default in production | Accepted (amended) |
| [D12](0012-test-sink.md) | A built-in test sink as the safe replay destination | Accepted |
| [D13](0013-frontend.md) | React, React Router, TanStack Query, Tailwind and shadcn/ui | Accepted |
| [D14](0014-hosting.md) | Deploy-ready for Cloud Run with Neon; not deployed yet | Accepted |
| [D15](0015-migrations.md) | Two migration owners, run in order | Accepted |
| [D16](0016-access-model.md) | No open sign-up; visitors use demo workspaces | Accepted |
| [D17](0017-limits.md) | All limits in one configuration module | Accepted |
| [D18](0018-deletion.md) | Permanent deletion through database cascades | Accepted |
| [D19](0019-repository.md) | A neutral, owner-independent repository | Accepted |
| [D20](0020-demo-video.md) | A short demo video, linked once it exists | Accepted, amended: committed to the repository |
