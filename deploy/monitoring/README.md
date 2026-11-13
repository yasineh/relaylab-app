# Monitoring templates (optional, not applied)

Optional Cloud Monitoring definitions for a RelayLab deployment. Nothing here
has been applied: RelayLab is not deployed yet (see `docs/deployment.md`).
The baseline is Cloud Run's and Neon's built-in metrics and notifications,
plus the structured logs and Error Reporting entries the services emit.

| File | What it creates |
|---|---|
| `uptime-check.json` | An HTTPS uptime check on `/readyz` of the app host, every 5 minutes |
| `alert-uptime.json` | An alert when that check fails from more than one region for 10 minutes |
| `alert-5xx.json` | An alert when `relaylab-app` serves more than about one 5xx response a minute for 5 minutes |

Placeholders, substituted with `envsubst` like the Cloud Run manifests:
`GCP_PROJECT`, `APP_HOST` (the `*.run.app` host, no scheme),
`UPTIME_CHECK_ID` (returned when the uptime check is created), and
`ALERT_NOTIFICATION_CHANNEL` (a channel's full resource name, for example an
email channel; the alert channel is the operator's choice and optional).

When `deploy-public-demo` applies them:

```sh
export GCP_PROJECT=... APP_HOST=... ALERT_NOTIFICATION_CHANNEL=...
envsubst < deploy/monitoring/uptime-check.json > /tmp/uptime.json
gcloud monitoring uptime create --project "$GCP_PROJECT" --config-from-file=/tmp/uptime.json
export UPTIME_CHECK_ID=...   # from the output above
for f in alert-uptime alert-5xx; do
  envsubst < "deploy/monitoring/$f.json" > "/tmp/$f.json"
  gcloud alpha monitoring policies create --project "$GCP_PROJECT" --policy-from-file="/tmp/$f.json"
done
```

`deploy/render.sh --check` renders these with placeholder values and checks
that they parse and name the notification channel.
