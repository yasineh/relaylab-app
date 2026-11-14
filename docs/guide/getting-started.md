# Getting started

RelayLab gives you a URL that records every HTTP request sent to it. You can
inspect each request exactly as it arrived and send it again. This guide
takes you from nothing to a received webhook in a few minutes.

## 1. Open RelayLab

- **On your machine:** follow the [development guide](../development/README.md#quick-start)
  (`pnpm install`, then `pnpm dev`) and open <http://localhost:5173>. Email
  goes to the local Mailpit inbox at <http://localhost:8025>.
- **A deployed instance:** open its address. RelayLab is not publicly deployed
  yet ([deployment status](../deployment.md)).

Then sign in in one of three ways:

- **Try the demo** (when the instance offers it): you get a workspace with a
  project, two endpoints, and sample webhooks. It is deleted after 24 hours,
  and replays go to its test sink only.
- **Create account** (when sign-up is open, as it is locally): enter a name,
  email, and a password of at least 12 characters, then open the verification
  link from the email.
- **An account from an operator** (sign-up is closed by default in
  production): `pnpm create-user -- --email you@example.com --name "Your Name"`
  reads the password from standard input.

## 2. Create a project

A project groups the endpoints for one integration, for example "Stripe
sandbox". On **Projects**, choose **Create project**, enter a name, and
choose **Create project** again. The project opens with its overview.

## 3. Create an endpoint

Open **Endpoints** in the project and choose **Create endpoint**. Give it a
name, for example "Payments". The endpoint page shows its **Receiving URL**:

```text
http://localhost:5173/h/4kQ9tZ…
```

Anyone with this URL can send requests to the endpoint, so treat it like a
password. Use **Copy URL**, and **Rotate URL** if it ever leaks.

## 4. Send a test webhook

Paste the URL into this command (or use **Copy curl command** on the endpoint
page) and run it:

```sh
ENDPOINT_URL='http://localhost:5173/h/<your token>'

curl -X POST "$ENDPOINT_URL/orders?attempt=1" \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer test-secret-123' \
  -d '{"order_id":"ord_123","amount":4200,"currency":"eur"}'
```

RelayLab answers once the request is stored:

```json
{"ok":true,"event_id":"019a6b1e-3c5d-7e8f-9a0b-1c2d3e4f5a6b"}
```

Anything after the token (`/orders`) and the query (`?attempt=1`) are kept
with the request. The `Authorization` value is removed before it is stored.

## 5. See it arrive

Open **Events**. The request is at the top of the list; if the page was
already open, it appeared without a reload. The status in the corner reads
**Live** while the page is connected.

To point a real provider at RelayLab, give it the receiving URL as its
webhook URL. On your own machine that URL is only reachable from your
machine; use a deployed instance for providers on the internet.

Next: [inspect and replay the request](webhook-workflow.md).
