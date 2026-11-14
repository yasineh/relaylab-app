# Product demo walkthrough

A 2–4 minute recording of RelayLab's core workflow: a webhook arrives, you
inspect it, and you replay it. This page lists every step, the synthetic
webhook, and the checks done before publishing. The recording is produced by
a script, so anyone can make it again from a clean checkout.

**Status:** recorded (2 min 36 s, 1920×1080) on the production-mode local
stack. It is not published yet: uploading it to YouTube as an unlisted video
is an action for the repository owner (see [Publishing](#publishing)).

## Record it

Requirements: Docker, Node.js, pnpm, and the Playwright browser
(`pnpm --filter @relaylab/e2e install-browser`). Nothing else is needed:
frames are encoded with the ffmpeg build that Playwright installs.

```sh
docker compose -f docker-compose.prod.yml up -d --build --wait
pnpm --filter @relaylab/e2e record-demo
docker compose -f docker-compose.prod.yml down -v
```

The video is written to `e2e/recordings/relaylab-demo.webm`. That folder is
not committed. To record against another instance, such as the public demo
once it is deployed, set `RELAYLAB_DEMO_URL=https://…`.

The script, [`e2e/tools/record-demo.ts`](../../e2e/tools/record-demo.ts),
drives Chromium through the real application at a 1536×864 viewport with
1.25× scaling, so the 1080p video has UI text a quarter larger than at 1×.
It adds four overlays and changes nothing else on the page:

- a caption bar describing each step;
- a pointer that follows the mouse, which a screen capture does not show;
- a terminal panel that prints the command it runs and the command's real
  output;
- a title card at the start and the end.

## The synthetic webhook

[`scripts/demo-send.sh`](../../scripts/demo-send.sh) sends it to a receiving
URL:

```sh
scripts/demo-send.sh [-k] <endpoint-url>
```

`-k` accepts the local stack's self-signed certificate. The request is:

- **Method and path:** `POST <endpoint-url>/orders/shipped?attempt=1&store=demo`
- **Headers:**
  - `Content-Type: application/json`
  - `User-Agent: ExampleShop-Webhooks/1.0`
  - `X-Example-Event: order.shipped`
  - `X-Example-Delivery: demo_delivery_0042`
  - `Authorization: Bearer demo_token_not_a_real_secret`. RelayLab removes
    this value on arrival, which is shown in the recording.
- **Body:** an `order.shipped` JSON object with an order id, carrier,
  tracking number, two line items, and a customer at `customer@example.com`.
  It uses only reserved example domains and `demo` identifiers.

The script prints RelayLab's acknowledgement (`{"ok":true,"event_id":"…"}`)
and the HTTP status.

## Steps and timings

| # | Time | Step | What is on screen |
|---|---|---|---|
| 1 | 0:00 | Title card, then the landing page, signed out | The headline, the request panel, and the start of "How it works" |
| 2 | 0:20 | **Try the demo** | A demo workspace opens on the event list, with seeded synthetic events. The header shows the demo visitor, and the banner says the data is synthetic and when it is deleted |
| 3 | 0:35 | The project: Overview, then Endpoints, then **Demo endpoint** | The project, its two endpoints, and the endpoint's receiving URL with its curl example |
| 4 | 0:55 | Events. The terminal panel runs `scripts/demo-send.sh -k <receiving URL>` | `HTTP 200` with the event id. `/orders/shipped` appears at the top of the live list with no reload, timed "now" |
| 5 | 1:15 | Open the event: Overview, Headers, Query, Body, Raw request | Method and path. `Authorization` reads "Removed at receipt". The `attempt` and `store` parameters. The JSON tree. The raw request |
| 6 | 1:55 | **Replay**, then **Send replay** | The destination is the test sink, with the request preview and the headers that will and won't be sent. The replay then shows "Succeeded · 200" |
| 7 | 2:20 | **Details** of the replay, then the end card | Replay history: state, target, method, timing, tries, response status, headers, and body |

## Content checklist

Checked against the recording of 2026-11-14, using frames sampled every
3 seconds:

- [x] Duration between 2 and 4 minutes (2:36)
- [x] The project is shown (step 3)
- [x] An endpoint is selected and its receiving URL shown (step 3)
- [x] A webhook is sent with a test sender, with its command and output visible (step 4)
- [x] The webhook arrives live, without a reload (step 4)
- [x] Method, headers, query parameters, and payload are inspected (step 5)
- [x] A replay is sent to a safe test destination, the built-in test sink (step 6)
- [x] The replay response and result are shown (step 7)
- [x] UI text is legible at 1080p
- [x] The video states where it was recorded: the end card says
  "production-mode stack"; also put this in the YouTube description

## Data-safety checklist

- [x] Every request, header, and body is synthetic: the demo seed and
  `demo-send.sh`, with reserved `example.com` domains and `demo` identifiers.
- [x] The only credential sent is a fake bearer token, and the recording
  shows it removed at receipt.
- [x] No real email address, name, or account appears. The account shown is
  an anonymous "Demo visitor".
- [x] The receiving URL shown belongs to a demo workspace on a local stack
  that is deleted with `down -v`, so it grants access to nothing.
- [x] The other addresses shown are local: `localhost:8443` and a Docker
  network address in `X-Forwarded-For`.
- [x] There is no browser chrome, bookmark bar, extension, or desktop in the
  frame: the capture is the page only.
- [x] The replay goes to the built-in test sink; nothing leaves RelayLab.

## Publishing

These steps need the owner's YouTube account:

1. Upload `e2e/recordings/relaylab-demo.webm` to YouTube as an **unlisted**
   video. A suggested description: "RelayLab receives a webhook, shows every
   byte, and replays it. Recorded on the production-mode stack with
   synthetic data."
2. Check that the link plays in a signed-out browser window.
3. Replace the "Demo recording" row of the README status table with the link.
4. Set `DEMO_VIDEO_URL` to the link in the deployment configuration
   (`deploy/cloudrun/app.yaml`, or `.env` locally). The landing page then
   shows a "Watch the demo" link.

Until then, the README marks the recording as not published and links to
this page.
