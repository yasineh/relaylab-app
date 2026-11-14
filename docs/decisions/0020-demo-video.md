# D20. A short demo video, linked once it exists

Status: Accepted, amended when the demo was recorded

## Context

Reviewers should be able to see the workflow without running anything.

## Decision

Record a 2–4 minute walkthrough on the production-mode stack, upload it to YouTube as unlisted, and link it from the README and the landing page (`DEMO_VIDEO_URL`).

**Amended (2026-11-14):** the owner chose to commit the recording to the repository instead, as [`docs/demo/relaylab-demo.webm`](../demo/relaylab-demo.webm), linked from the README and the [walkthrough](../demo/walkthrough.md). A public video host is optional. The landing page links a video only once it has a public URL, through `DEMO_VIDEO_URL`.

## Consequences

- The recording is produced by a script (`pnpm --filter @relaylab/e2e record-demo`), so it can be made again after UI changes.
- The repository carries an 8 MB binary; a new recording replaces it.
- While `DEMO_VIDEO_URL` is unset, the landing page shows no video link.
