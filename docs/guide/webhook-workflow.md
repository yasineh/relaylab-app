# Inspecting and replaying webhooks

This guide covers what RelayLab does with a request, how to read it, and how
to send it again. It assumes you have an endpoint and have sent it a request
([Getting started](getting-started.md)).

## How a request is received

When a request reaches an endpoint's URL, RelayLab:

1. Checks the endpoint exists and is enabled (`404` or `410` otherwise), and
   applies the size and rate limits (`413`, `429` with `Retry-After`).
2. Replaces the values of credential headers and query parameters, such as
   `Authorization`, `Cookie`, API keys, signatures, and `token`, with
   `[REDACTED]`. The original values are never stored.
3. Stores the method, path, query, headers, the body byte for byte, the
   content type, the time, the user agent, and the sender's network.
4. Answers `200 {"ok":true,"event_id":"…"}` and shows the request to anyone
   watching the project's events.

Every method from `GET` to `OPTIONS` is accepted, and any content type:
JSON, forms, plain text, or binary. The full contract, including every status
code, is in the [API reference](../api.md#webhook-ingestion-h).

**Payloads may contain confidential or personal data.** Bodies are stored
exactly as sent and are deleted after the retention period shown on the
Events page.

## Find a request

**Events** lists every request in the project, newest first. Filter by
endpoint, method, time range, or content type, and switch to oldest first.
**Load more** at the bottom fetches older requests.

## Inspect a request

Open a request to see its details. The heading is the method and the path
with its query, and the tabs show:

| Tab | What it shows |
|---|---|
| **Overview** | Time, endpoint, method, path, content type, body size, how many values were removed, the sender's network, user agent, and ids |
| **Body** | JSON as a collapsible tree or as raw text; other text as raw text; binary as hex. **Download raw** saves the exact bytes. JSON that does not parse is shown as received, with a notice |
| **Headers** | Every header, sorted by name. Removed values read **Removed at receipt** |
| **Query** | The raw query string and each parameter |
| **Raw request** | The request line, headers, and body as text |

Each tab has a copy button, and **Copy as curl** gives a command that sends
the same request again (removed values stay `[REDACTED]`).

## Replay a request

Choose **Replay** on the request. The dialog explains that a replay re-sends
the stored body but cannot reproduce the original signature or
authentication.

1. **Destination.**
   - **Test sink** sends the request to another endpoint of the same project
     and stores it there as a new request, without leaving RelayLab. This is
     the default, and it always works.
   - **URL** sends it to an HTTPS address you control, for example your own
     webhook handler. It must be a public address: private networks,
     localhost, and cloud metadata addresses are refused, and redirects are
     not followed. URL replay may be turned off on an instance, and demo
     workspaces can only use the test sink.
2. **Method.** The original method by default.
3. **Added headers.** Credential and signature headers from the original are
   not sent. Add the ones your handler needs, for example a test
   `Authorization` value. Added values are stored encrypted and erased once
   the replay finishes.
4. Check the **Request preview**, which shows exactly what will be sent, and
   choose **Send replay**.

The replay runs in the background. Connection failures, DNS failures,
timeouts, and `502`, `503`, or `504` responses are retried, up to three tries
in all.

## Inspect the result

The request's **Replays** panel lists each replay with its state: **Queued**,
**Running**, **Succeeded · 200** (any HTTP status counts as delivered), or
**Failed** with the reason, such as "non-public address", "timed out", or
"connection failed". **Details** opens the replay in **Replays** (the
project's replay history), which shows the target, method, timing, tries,
and the response status, headers, and body (up to 64 KiB, shown as text).

A replay to the test sink also appears as a new request on the sink endpoint,
marked as a replay, so you can inspect what was delivered.

## Delete requests

**Delete event** on a request removes it and its replays. **Clear events** on
an endpoint removes everything it received. Both are permanent. Requests are
also deleted automatically after the retention period
([details](../security/data-retention.md)).
