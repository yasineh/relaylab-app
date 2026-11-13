#!/usr/bin/env bash
# Smoke test for the production-mode local stack (docker-compose.prod.yml).
# Needs only Docker, curl, and jq: no cloud account or credentials.
#
#   docker compose -f docker-compose.prod.yml up -d --build --wait
#   scripts/prod-smoke.sh
#
# Checks: health and readiness through TLS; production config; a demo start
# with Secure cookies and a seeded workspace; ingestion; live arrival over
# SSE; replay to the test sink (through the transaction pooler); URL replay
# refused for demo users and, with replay off, for real users; sign-up
# refused; no debug routes; and no secret in any service's logs.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="${PROD_BASE_URL:-https://localhost:${PROD_HTTPS_PORT:-8443}}"
COMPOSE=(docker compose -f "$ROOT/docker-compose.prod.yml")
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
JAR="$WORK/demo.jar"
JAR2="$WORK/real.jar"
pass=0

ok() { pass=$((pass + 1)); printf '  ok  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1" >&2; exit 1; }
# -k: Caddy's local certificate is not trusted by this machine.
req() { curl -sk -o "$WORK/body" -w '%{http_code}' -H "Origin: $BASE" "$@"; }
body() { cat "$WORK/body"; }

echo "Smoke-testing $BASE"

[ "$(req "$BASE/healthz")" = 200 ] || fail "healthz"
[ "$(req "$BASE/readyz")" = 200 ] || fail "readyz: $(body)"
ok "health and readiness over TLS"

[ "$(req "$BASE/api/v1/config")" = 200 ] || fail "config"
jq -e '.signup_enabled == false and .replay_enabled == false and .demo_enabled == true' "$WORK/body" >/dev/null ||
  fail "production defaults: $(body)"
ok "production defaults: sign-up closed, URL replay off, demos on"

# --- Demo workspace --------------------------------------------------------
[ "$(req -c "$JAR" -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/auth/sign-in/anonymous")" = 200 ] ||
  fail "demo start: $(body)"
grep -q '__Secure-relaylab.session_token' "$JAR" || fail "the session cookie is not a __Secure- cookie"
awk '$6 == "__Secure-relaylab.session_token" && $4 != "TRUE" { exit 1 }' "$JAR" || fail "the session cookie is not Secure"
ok "demo started; the session cookie is __Secure- and Secure"

[ "$(req -b "$JAR" "$BASE/api/v1/me")" = 200 ] || fail "me"
jq -e '.is_demo == true and (.demo_expires_at | length > 0)' "$WORK/body" >/dev/null || fail "demo identity: $(body)"
[ "$(req -b "$JAR" "$BASE/api/v1/projects")" = 200 ] || fail "projects"
PROJECT="$(jq -r '.items[0].id' "$WORK/body")"
[ "$(jq -r '.items[0].name' "$WORK/body")" = "Demo project" ] || fail "no demo project: $(body)"
[ "$(req -b "$JAR" "$BASE/api/v1/projects/$PROJECT/events?limit=100")" = 200 ] || fail "events"
SEEDED="$(jq '.items | length' "$WORK/body")"
[ "$SEEDED" -ge 8 ] || fail "only $SEEDED seeded events"
EVENT="$(jq -r '.items[0].id' "$WORK/body")"
ok "workspace seeded: Demo project with $SEEDED synthetic events"

[ "$(req -b "$JAR" "$BASE/api/v1/projects/$PROJECT/endpoints")" = 200 ] || fail "endpoints"
HOOK_URL="$(jq -r '.items[] | select(.name == "Demo endpoint") | .url' "$WORK/body")"
SINK="$(jq -r '.items[] | select(.name == "Test sink") | .id' "$WORK/body")"
[ -n "$HOOK_URL" ] && [ -n "$SINK" ] || fail "endpoints: $(body)"
TOKEN="${HOOK_URL##*/}"

# --- Live arrival over SSE, then ingestion ----------------------------------
curl -skN -b "$JAR" -H "Origin: $BASE" --max-time 8 "$BASE/api/v1/projects/$PROJECT/events/stream" > "$WORK/sse" 2>/dev/null &
SSE_PID=$!
sleep 2
[ "$(req -X POST -H 'Content-Type: application/json' -H 'Authorization: Bearer smoke-secret-auth-value' \
  -d '{"smoke":"prod"}' "$HOOK_URL/smoke")" = 200 ] || fail "ingestion: $(body)"
NEW_EVENT="$(jq -r '.event_id' "$WORK/body")"
ok "webhook accepted ($NEW_EVENT)"
for _ in $(seq 1 20); do grep -q "$NEW_EVENT" "$WORK/sse" && break; sleep 0.3; done
kill "$SSE_PID" 2>/dev/null || true
wait "$SSE_PID" 2>/dev/null || true
grep -q "$NEW_EVENT" "$WORK/sse" || fail "the event did not arrive on the live stream"
ok "event arrived live over SSE (LISTEN on the direct connection)"

# --- Replay -----------------------------------------------------------------
REPLAYS="$BASE/api/v1/projects/$PROJECT/events/$EVENT/replays"
[ "$(req -b "$JAR" -X POST -H 'Content-Type: application/json' -H "Idempotency-Key: smoke-$$-1" \
  -d '{"destination":{"type":"url","url":"https://example.com/hook"}}' "$REPLAYS")" = 403 ] || fail "demo URL replay: $(body)"
jq -e '.error.code == "demo_restricted"' "$WORK/body" >/dev/null || fail "demo URL replay code: $(body)"
ok "URL replay refused for the demo user (403 demo_restricted)"

SINK_BODY="$(jq -nc --arg id "$SINK" '{destination: {type: "sink", endpoint_id: $id}}')"
[ "$(req -b "$JAR" -X POST -H 'Content-Type: application/json' -H "Idempotency-Key: smoke-$$-2" \
  -d "$SINK_BODY" "$REPLAYS")" = 202 ] || fail "sink replay: $(body)"
REPLAY="$(jq -r '.id' "$WORK/body")"
state=""
for _ in $(seq 1 30); do
  req -b "$JAR" "$BASE/api/v1/projects/$PROJECT/replays/$REPLAY" >/dev/null
  state="$(jq -r '.state' "$WORK/body")"
  [ "$state" = succeeded ] && break
  sleep 0.5
done
[ "$state" = succeeded ] || fail "sink replay ended $state: $(body)"
ok "replay to the test sink succeeded (worker through the transaction pooler)"

# --- A real account (operator CLI; sign-up is closed) -----------------------
REAL_EMAIL="smoke-$$@example.test"
REAL_PASSWORD="smoke-orchid-41-lantern-$$"
printf '%s\n' "$REAL_PASSWORD" | "${COMPOSE[@]}" exec -T auth node src/cli/create-user.ts \
  --email "$REAL_EMAIL" --name "Smoke Tester" >/dev/null || fail "create-user"
SIGNIN_BODY="$(jq -nc --arg e "$REAL_EMAIL" --arg p "$REAL_PASSWORD" '{email: $e, password: $p}')"
[ "$(req -c "$JAR2" -X POST -H 'Content-Type: application/json' -d "$SIGNIN_BODY" "$BASE/api/auth/sign-in/email")" = 200 ] ||
  fail "sign-in: $(body)"
[ "$(req -b "$JAR2" -X POST -H 'Content-Type: application/json' -d '{"name":"Smoke"}' "$BASE/api/v1/projects")" = 201 ] ||
  fail "create project: $(body)"
RPROJECT="$(jq -r '.id' "$WORK/body")"
[ "$(req -b "$JAR2" -X POST -H 'Content-Type: application/json' -d '{"name":"Smoke"}' \
  "$BASE/api/v1/projects/$RPROJECT/endpoints")" = 201 ] || fail "create endpoint: $(body)"
RHOOK="$(jq -r '.url' "$WORK/body")"
[ "$(req -X POST -d 'x' "$RHOOK")" = 200 ] || fail "real ingestion"
REVENT="$(jq -r '.event_id' "$WORK/body")"
[ "$(req -b "$JAR2" -X POST -H 'Content-Type: application/json' -H "Idempotency-Key: smoke-$$-3" \
  -d '{"destination":{"type":"url","url":"https://example.com/hook"}}' \
  "$BASE/api/v1/projects/$RPROJECT/events/$REVENT/replays")" = 503 ] || fail "URL replay while off: $(body)"
jq -e '.error.code == "replay_unavailable"' "$WORK/body" >/dev/null || fail "URL replay code: $(body)"
ok "URL replay refused while REPLAY_ENABLED is off (503 replay_unavailable)"

# --- Sign-up closed ---------------------------------------------------------
SIGNUP_EMAIL="smoke-signup-$$@example.test"
SIGNUP_BODY="$(jq -nc --arg e "$SIGNUP_EMAIL" '{email: $e, password: "smoke-heron-77-copper", name: "Nope"}')"
code="$(req -X POST -H 'Content-Type: application/json' -d "$SIGNUP_BODY" "$BASE/api/auth/sign-up/email")"
[ "${code:0:1}" = 4 ] || fail "sign-up answered $code"
users="$("${COMPOSE[@]}" exec -T postgres psql -U postgres -d relaylab -Atc \
  "SELECT count(*) FROM auth.\"user\" WHERE email = '$SIGNUP_EMAIL'")"
[ "$users" = 0 ] || fail "sign-up created a user"
ok "sign-up refused ($code) and no user created"

# --- No debug routes --------------------------------------------------------
for path in /debug/pprof/ /debug/vars; do
  [ "$(req "$BASE$path")" = 404 ] || fail "$path is reachable"
  jq -e '.error.code == "not_found"' "$WORK/body" >/dev/null || fail "$path: $(body)"
done
ok "debug routes absent (404 with the error envelope)"

# --- Logs hold no secrets ---------------------------------------------------
"${COMPOSE[@]}" logs --no-color api worker auth > "$WORK/logs" 2>&1
SESSION="$(awk '$6 == "__Secure-relaylab.session_token" { print $7 }' "$JAR" | cut -d. -f1)"
for secret in smoke-secret-auth-value "$REAL_PASSWORD" "$TOKEN" "$SESSION" \
  prod-local-only-app-runtime prod-local-only-auth-runtime prod-local-only-better-auth-secret \
  prod-local-only-auth-proxy-secret ZGV2LW9ubHkt; do
  [ -n "$secret" ] || continue
  if grep -qF -- "$secret" "$WORK/logs"; then fail "a secret appears in the logs (${secret:0:12}...)"; fi
done
grep -q '"severity":"INFO"' "$WORK/logs" || fail "no Cloud Logging severity in the logs"
ok "logs contain no secret, token, password, or credential header; entries carry severity"

echo "All $pass checks passed."
