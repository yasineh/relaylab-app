# Received events: storage, redaction, and display

How RelayLab handles the webhook requests it receives, from arrival to the
event explorer. API details are in the [API reference](../api.md#events).

## What is stored, and what is not

- Each accepted request is stored as one event: method, path after the token,
  query string, headers, the body bytes exactly as received, size, receive
  time (UTC), the sender's address **truncated** to an IPv4 /24 or IPv6 /48
  network, the user agent, and the request id.
- **Credential values are removed on arrival**, before the event is stored,
  logged, or published. The values of headers such as `Authorization` and
  `Cookie`, provider signature headers, and query parameters such as `token`
  are replaced with `[REDACTED]`. The full rule list is in the
  [API reference](../api.md#webhook-ingestion-h). Names stay visible.
  **Originals are never stored**, so nobody can recover them, including the
  project owner, an operator, or RelayLab itself.
- **Bodies are not redacted.** They are kept byte for byte, so they may contain
  confidential or personal data. The explorer says so on every event.

## Who can read events

- Only the owner of the project, through a session validated by the Go API.
  The caller comes only from that session, never from request parameters.
- Every event query joins the caller's projects in SQL, and the endpoint
  filter checks that the endpoint belongs to the requested project. Another
  user's event, an event in another project, and a missing event all return
  the same `404 not_found`, so ownership cannot be probed.
- Page cursors are not signed. They are opaque, and the query that uses one is
  still limited to the caller's projects, so an edited or foreign cursor can
  only move within events the caller may already read.
- The live event stream follows the same rules. It opens only for the
  project's owner, re-checks the session and ownership at least every minute
  (and at session expiry), and closes when either is gone. It sends only list
  summaries (method, path, endpoint name, content type, size, time), never
  bodies, headers, or query values. Received text is JSON-encoded on one line,
  so it can't inject stream messages.
- Public ingestion (`/h/{token}`) can only add events. It cannot read, list, or
  change them.

## How removed values appear

- The API returns each removed value as `"[REDACTED]"` with `"redacted": true`,
  and `redacted_count` says how many values were removed. There is no reveal
  option. A `reveal` parameter is ignored.
- The explorer shows a lock icon and the text "Removed at receipt" (not colour
  alone), with an explanation that the value was present but not stored.
- Copied headers, query parameters, the raw request, and the reproduction curl
  command contain `[REDACTED]`, and the page notes that they do not carry the
  original credentials.

## Rendering untrusted content

Everything in an event is attacker-controlled: paths, headers, query values,
user agents, and bodies.

- The web app renders received content only as React text nodes.
  `dangerouslySetInnerHTML` is banned by lint (`react/no-danger`), and tests
  render `<script>`, `<img onerror>`, `<svg onload>`, and `javascript:` URLs
  as inert text. Received values are never linked or used in URLs, styles, or
  attributes.
- Every received string (paths, header and query names and values, content
  types, JSON keys and values, and raw text) shows control characters and
  Unicode bidirectional overrides as highlighted escapes (for example
  `\u202e`), so text cannot display differently from what it contains.
- Dashboard pages are served with
  `Content-Security-Policy: default-src 'self'; script-src 'self'; object-src 'none'; frame-ancestors 'none'`
  (plus related directives) and `X-Content-Type-Options: nosniff`.
- The raw body download is `application/octet-stream` with
  `Content-Disposition: attachment`, `nosniff`, and `Content-Security-Policy:
  sandbox`, so a browser never renders a received HTML or SVG body.
- The reproduction curl command single-quotes every value (POSIX), and binary
  bodies are piped through `printf` with octal escapes. Tests run generated
  commands in a real shell to prove values arrive literally.

## Retention and deletion

Events are deleted automatically after the retention period (7 days by
default), beyond a per-project cap (10,000 by default), and with their endpoint
or project (database cascade). Owners can also delete one event or clear an
endpoint's events. See [Data retention and deletion](data-retention.md).
