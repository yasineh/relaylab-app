# Testing

RelayLab has three layers of tests. Run the first two before every push; the
end-to-end suite needs Docker and takes about a minute once images are built.

| Layer | Command | Needs |
|---|---|---|
| Unit and integration (Go, web, auth) | `pnpm test` | Docker (starts the `postgres-test` container) |
| All local checks: format, lint, types, tests, build | `pnpm check` | Docker |
| End-to-end, in a real browser against the full stack | `pnpm e2e` | Docker, Chromium for Playwright |

The unit and integration suites are described in
[Development: Tests](../development.md#tests). This page covers the
end-to-end suite.

## End-to-end suite

`e2e/` is a Playwright project (TypeScript, Chromium). It drives the real web
app served by the real api, signs up through the real Better Auth service,
reads verification emails from Mailpit, and replays to a controlled echo
service. Nothing is mocked and nothing leaves your machine.

### First run

```sh
pnpm install
pnpm --filter @relaylab/e2e install-browser   # downloads Chromium for Playwright, once
pnpm e2e                                       # builds and starts the stack, then runs the suite
```

`pnpm e2e` runs `stack:up` (build the images, start `docker-compose.e2e.yml`,
wait until every service is healthy) and then the tests. Afterwards:

```sh
pnpm e2e:down    # stop the stack and delete its data
```

While iterating, keep the stack running and run only the tests:

```sh
pnpm --filter @relaylab/e2e e2e                        # whole suite
pnpm --filter @relaylab/e2e e2e -- negatives           # one file
pnpm --filter @relaylab/e2e e2e -- --project=chromium  # skip the stack-restarting tests
pnpm --filter @relaylab/e2e exec playwright show-report
```

Rebuild the stack (`pnpm --filter @relaylab/e2e stack:up`) after changing Go,
web, or auth code: the suite tests the built images, not your dev servers.

### The stack

`docker-compose.e2e.yml` is standalone (Compose project `relaylab-e2e`). It
never touches the development stack's containers, volumes, or ports.

| Service | Host port | Notes |
|---|---|---|
| `api` | 4180 | Serves the built web app; the browser and the suite use it |
| `api-ingest` | 4181 | A second api instance on the same database, used to send webhooks while `api` restarts |
| `mailpit` | 4125 | Inbox API for verification emails |
| `postgres` | 4132 | In-memory storage. The suite connects as a superuser only to expire a session |
| `auth`, `worker`, `echo` | none | Internal network only |

Override a port with `E2E_API_PORT`, `E2E_INGEST_PORT`, `E2E_MAILPIT_PORT`, or
`E2E_POSTGRES_PORT`, set the same way for both `stack:up` and the tests.

**Offline by design.** The `internal` network has no route to the internet.
The worker, which sends replays, and the auth service are only on it. A test
proves a replay to a public host fails to connect. The api, Mailpit, and
Postgres also join a second network only so their ports can be published to
127.0.0.1.

**Test configuration.** All secrets in the file are fixed, non-secret
placeholders. Limits are lowered so negative cases trigger quickly: a 64 KiB
body limit, an endpoint burst of 10 requests and 6 per minute, and a 2-second
stream revalidation. Better Auth's own per-IP rate limits are off, because
every test signs up from the same address; the auth service's tests cover
them.

**The echo destination.** `e2e/echo` answers every request with `200` and a
JSON summary, and logs one line per request. The worker can reach it only
because `REPLAY_TEST_ALLOWLIST=echo:8080` is set. That setting lets exactly
the listed host:port through the outbound guard, over plain HTTP, at the
addresses the name resolves to. The api and worker refuse to start with it
when `APP_ENV=production`, and it can't name IP literals, RelayLab's own
hosts, the database, or metadata hosts. See
[Outbound requests](../security/outbound-replay.md#test-only-allowlist).

### What is covered

| File | Covers |
|---|---|
| `journey.spec.ts` | The critical journey: sign up in the UI, verify the email through Mailpit, create a project and two endpoints, send a webhook and see it arrive live (≤5 s, no reload), inspect method, headers, query, and body, replay to the test sink and to the echo service, and read the response in replay history |
| `live-stream.spec.ts` | Several webhooks arrive live; signing out in another tab ends the stream and redirects to sign-in with the expired notice |
| `negatives.spec.ts` | Malformed JSON shown raw with a parse notice; another user's project, events, endpoints, and replays show "Project not found" and no data; signed-out redirect; tampered and expired sessions (UI redirect and `401`); `413` for an oversized body; `429` with `Retry-After` beyond the endpoint rate limit; metadata, loopback, private, and plain-HTTP replay destinations refused in the dialog and by the API, with no replay created and no outbound attempt in the worker log |
| `privacy.spec.ts` | `Authorization`, `Cookie`, and `X-Api-Key` values never appear in any tab, the copied headers, the curl command, or the raw request; their names show "Removed at receipt" |
| `offline.spec.ts` | The worker has no internet route |
| `landing.spec.ts` | The landing page at 320, 375, 768, 1280, and 1920 px: no horizontal overflow, "Try the demo" visible, full-page screenshots attached to the report; the HTML's title, description, and Open Graph tags; keyboard order starting with the skip link, with visible focus |
| `stack.disruptive.ts` | Stops the api mid-stream and checks that webhooks received meanwhile appear after reconnecting, without a reload. Restarts auth and the api with `AUTH_SIGNUP_ENABLED=false` and checks that sign-up is refused in the UI and the API and no account is created, then turns sign-up back on |

The `disruptive` Playwright project depends on the `chromium` project, so it
runs only after every other test, one test at a time.

### Landing page screenshots

The landing page's product screenshots and its Open Graph image are real
RelayLab screens of a demo workspace (synthetic data). Regenerate them after
UI changes, with the e2e stack running:

```sh
pnpm --filter @relaylab/e2e screenshots
```

It writes `web/src/assets/screenshots/*.jpg` and `web/public/og-image.png`.

### Rules for new tests

- **Isolation.** Each test signs up its own user (`e2e+<uuid>@example.test`)
  and creates its own projects and endpoints. Never rely on data from another
  test or an earlier run; the stack is not reset between runs.
- **No fixed sleeps.** Wait for visible state with Playwright's auto-waiting
  assertions or `expect.poll`.
- **Send webhooks as bytes.** Use `sendWebhook` from `fixtures/app.ts`. It
  sends string bodies byte for byte, because Playwright would otherwise
  re-encode some strings as JSON.
- **Anything that restarts or reconfigures a service** goes in
  `stack.disruptive.ts`.
- **No retries in CI** (`CI=1`): a flaky test fails the run, so fix it rather
  than retrying.

### In CI

The `End-to-end` job in `.github/workflows/ci.yml` runs the same commands with
`CI=1` and counts toward `ci-success`. On failure it uploads the HTML report,
traces, screenshots, and the stack logs as the `e2e-report` artifact. CI runs
only when started manually (see [CI](../ci.md)).
