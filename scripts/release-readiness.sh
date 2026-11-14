#!/usr/bin/env bash
# Pre-deploy readiness review (release-hardening-review, section 2) against
# the production-mode local stack. It records what it sends and what comes
# back, so its output is the evidence in docs/release/.
#
#   docker compose -f docker-compose.prod.yml up -d --build --wait
#   scripts/release-readiness.sh | tee docs/release/evidence/readiness-<date>.log
#
# It turns URL replay on for the SSRF section (deploy/local/prod/replay-on.yml)
# and restores the stack's defaults at the end. It backdates rows to test the
# purges, so run it only against this throwaway local stack.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="${PROD_BASE_URL:-https://localhost:${PROD_HTTPS_PORT:-8443}}"
COMPOSE=(docker compose -f "$ROOT/docker-compose.prod.yml")
REPLAY_ON=(docker compose -f "$ROOT/docker-compose.prod.yml" -f "$ROOT/deploy/local/prod/replay-on.yml")
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
passed=0
failed=0

pass() { passed=$((passed + 1)); printf 'PASS  %s\n' "$1"; }
fail() { failed=$((failed + 1)); printf 'FAIL  %s\n' "$1"; }
check() { if [ "$2" = "$3" ]; then pass "$1 ($2)"; else fail "$1: got $2, want $3"; fi; }
section() { printf '\n## %s\n' "$1"; }
# req [curl args...]: prints the status code; the body is in $WORK/body.
req() { curl -sk -o "$WORK/body" -w '%{http_code}' "$@"; }
code() { jq -r '.error.code // empty' "$WORK/body" 2>/dev/null; }
sql() { "${COMPOSE[@]}" exec -T postgres psql -U postgres -d relaylab -Atc "$1"; }
as_role() { "${COMPOSE[@]}" exec -T -e PGPASSWORD="$2" postgres psql -h 127.0.0.1 -U "$1" -d relaylab -Atc "$3" 2>&1; }
api() { req -H "Origin: $BASE" -H 'Content-Type: application/json' "$@"; }

echo "# RelayLab readiness review"
echo "Target: $BASE"
echo "Commit: $(git -C "$ROOT" rev-parse --short HEAD)$(git -C "$ROOT" diff --quiet HEAD -- backend auth web || echo ' (with local changes)')"
echo "Date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"

# --- Accounts: two real users through the operator CLI (sign-up is closed) --
new_user() { # name -> prints the cookie jar path
  local email="review-$1-$$@example.test" password="review-$1-cobalt-$$-$RANDOM"
  printf '%s\n' "$password" | "${COMPOSE[@]}" exec -T auth node src/cli/create-user.ts \
    --email "$email" --name "Review $1" >/dev/null
  local body
  body="$(jq -nc --arg e "$email" --arg p "$password" '{email: $e, password: $p}')"
  [ "$(api -c "$WORK/$1.jar" -X POST -d "$body" "$BASE/api/auth/sign-in/email")" = 200 ] || { echo "sign-in $1 failed" >&2; exit 1; }
  echo "$WORK/$1.jar"
}
A="$(new_user alice)"
B="$(new_user bob)"
api -b "$A" -X POST -d '{"name":"Review A"}' "$BASE/api/v1/projects" >/dev/null
PA="$(jq -r .id "$WORK/body")"
api -b "$A" -X POST -d '{"name":"Inbound"}' "$BASE/api/v1/projects/$PA/endpoints" >/dev/null
EA="$(jq -r .id "$WORK/body")"
HOOK="$(jq -r .url "$WORK/body")"
api -b "$A" -X POST -d '{"name":"Sink"}' "$BASE/api/v1/projects/$PA/endpoints" >/dev/null
SINK="$(jq -r .id "$WORK/body")"
req -X POST -H 'Content-Type: application/json' -d '{"order":"demo-1"}' "$HOOK/orders" >/dev/null
EVA="$(jq -r .event_id "$WORK/body")"
api -b "$A" -X POST -H "Idempotency-Key: $(uuidgen)" \
  -d "{\"destination\":{\"type\":\"sink\",\"endpoint_id\":\"$SINK\"}}" "$BASE/api/v1/projects/$PA/events/$EVA/replays" >/dev/null
RA="$(jq -r .id "$WORK/body")"
echo "Setup: users A and B, A's project, endpoint, sink, event, and replay created"

# --- 2.1 Authentication and authorization ------------------------------------
section "2.1 Authentication and authorization"
check "no session: GET /api/v1/projects" "$(req "$BASE/api/v1/projects")" 401
check "forged cookie: GET /api/v1/me" "$(req -H 'Cookie: __Secure-relaylab.session_token=forged.value' "$BASE/api/v1/me")" 401
for path in "projects/$PA" "projects/$PA/endpoints/$EA" "projects/$PA/events" "projects/$PA/events/$EVA" \
  "projects/$PA/events/$EVA/body" "projects/$PA/replays/$RA" "projects/$PA/replays" "projects/$PA/audit-log"; do
  check "B reads A's /$path" "$(api -b "$B" "$BASE/api/v1/$path")" 404
done
check "B replays A's event" "$(api -b "$B" -X POST -H "Idempotency-Key: $(uuidgen)" \
  -d "{\"destination\":{\"type\":\"sink\",\"endpoint_id\":\"$SINK\"}}" "$BASE/api/v1/projects/$PA/events/$EVA/replays")" 404
check "B renames A's project" "$(api -b "$B" -X PATCH -d '{"name":"x"}' "$BASE/api/v1/projects/$PA")" 404
check "B deletes A's event" "$(api -b "$B" -X DELETE "$BASE/api/v1/projects/$PA/events/$EVA")" 404
check "B deletes A's project" "$(api -b "$B" -X DELETE "$BASE/api/v1/projects/$PA")" 404
check "A's project still exists after B's attempts" "$(api -b "$A" "$BASE/api/v1/projects/$PA")" 200
check "B's stream of A's project" "$(req -b "$B" --max-time 3 "$BASE/api/v1/projects/$PA/events/stream")" 404
check "unsafe method without Origin" "$(req -b "$A" -X POST -H 'Content-Type: application/json' -d '{"name":"x"}' "$BASE/api/v1/projects")" 403
echo "      code: $(code)"
check "unsafe method from another origin" "$(req -b "$A" -X POST -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{"name":"x"}' "$BASE/api/v1/projects")" 403
check "auth API from another origin (sign-out)" "$(req -b "$A" -X POST -H 'Origin: https://evil.example' "$BASE/api/auth/sign-out")" 403
cp "$A" "$WORK/a-copy.jar"
api -b "$A" -X POST "$BASE/api/auth/sign-out" >/dev/null
check "a signed-out session cookie is rejected" "$(req -b "$WORK/a-copy.jar" "$BASE/api/v1/me")" 401
A="$(new_user alice2)"
api -b "$A" -X POST -d '{"name":"Review A2"}' "$BASE/api/v1/projects" >/dev/null
PA="$(jq -r .id "$WORK/body")"
api -b "$A" -X POST -d '{"name":"Inbound"}' "$BASE/api/v1/projects/$PA/endpoints" >/dev/null
EA="$(jq -r .id "$WORK/body")"
HOOK="$(jq -r .url "$WORK/body")"

# --- 2.2 Ingestion limits and redaction ---------------------------------------
section "2.2 Ingestion limits, redaction, and malformed input"
head -c 262144 /dev/zero | tr '\0' 'a' >"$WORK/limit.bin"
head -c 262145 /dev/zero | tr '\0' 'a' >"$WORK/over.bin"
check "body of exactly 256 KiB" "$(req -X POST --data-binary @"$WORK/limit.bin" "$HOOK")" 200
check "body of 256 KiB + 1" "$(req -X POST --data-binary @"$WORK/over.bin" "$HOOK")" 413
headers=()
for i in $(seq 1 101); do headers+=(-H "X-Field-$i: v"); done
check "101 header fields" "$(req -X POST "${headers[@]}" -d x "$HOOK")" 431
check "34 KiB of headers (over LIMIT_HTTP_MAX_HEADER_BYTES)" "$(req -X POST -H "X-Big: $(head -c 34816 /dev/zero | tr '\0' 'b')" -d x "$HOOK")" 431
check "  answered by the API's limit" "$(code)" headers_too_large
check "40 KiB of headers (the server's hard backstop)" "$(req -X POST -H "X-Big: $(head -c 40960 /dev/zero | tr '\0' 'b')" -d x "$HOOK")" 431
# The API answers a stalled body with 408 (checked from inside the stack's
# network). A reverse proxy may report the API's early reply differently while
# it is still forwarding the body: the local Caddy answers 502 (finding F4).
NET="$(docker inspect "$("${COMPOSE[@]}" ps -q api)" --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}')"
status="$(docker run --rm --network "$NET" -e HOOK_PATH="${HOOK#"$BASE"}" --entrypoint sh \
  alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf -c \
  'apk add -q --no-cache curl >/dev/null; { printf abc; sleep 13; } |
   curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Length: 100" --data-binary @- "http://api:8080$HOOK_PATH"')"
check "a body that stops arriving, at the API (Content-Length 100, 3 bytes sent)" "$status" 408
status="$({ printf 'abc'; sleep 13; } | curl -sk -o /dev/null -w '%{http_code}' -X POST -H 'Content-Length: 100' --data-binary @- "$HOOK")"
echo "      the same through the local Caddy proxy: $status (finding F4)"
check "unsupported method TRACE" "$(req -X TRACE "$HOOK")" 405
check "unknown endpoint token" "$(req -X POST -d x "$BASE/h/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")" 404
check "invalid JSON is stored as sent" "$(req -X POST -H 'Content-Type: application/json' -d '{"broken": ' "$HOOK")" 200
BROKEN="$(jq -r .event_id "$WORK/body")"
check "  json_invalid recorded" "$(sql "SELECT json_invalid FROM app.webhook_events WHERE id = '$BROKEN'")" t
printf '\xff\xfe\x00binary\x80' >"$WORK/bin"
check "non-UTF-8 body" "$(req -X POST -H 'Content-Type: application/octet-stream' --data-binary @"$WORK/bin" "$HOOK")" 200
BIN="$(jq -r .event_id "$WORK/body")"
check "  stored byte for byte" "$(sql "SELECT encode(body, 'hex') || ' ' || body_encoding FROM app.webhook_events WHERE id = '$BIN'")" "fffe0062696e61727980 binary"
SECRETS=(review-bearer-7c1 review-cookie-9d2 review-apikey-3e4 review-sig-5f6 review-query-8a9)
check "request with credentials in headers and query" "$(req -X POST \
  -H "Authorization: Bearer ${SECRETS[0]}" -H "Cookie: sid=${SECRETS[1]}" -H "X-Api-Key: ${SECRETS[2]}" \
  -H "Stripe-Signature: t=1,v1=${SECRETS[3]}" -d x "$HOOK/creds?token=${SECRETS[4]}&page=2")" 200
CREDS="$(jq -r .event_id "$WORK/body")"
leaks=0
for s in "${SECRETS[@]}"; do
  n="$(sql "SELECT count(*) FROM app.webhook_events WHERE headers::text LIKE '%$s%' OR raw_query LIKE '%$s%' OR query_params::text LIKE '%$s%' OR path LIKE '%$s%'")"
  [ "$n" = 0 ] || leaks=$((leaks + 1))
done
check "credential values found anywhere in app.webhook_events" "$leaks" 0
check "  redacted values recorded for that event" "$(sql "SELECT redacted_header_count FROM app.webhook_events WHERE id = '$CREDS'")" 4
echo "      stored query: $(sql "SELECT raw_query FROM app.webhook_events WHERE id = '$CREDS'")"
log_leaks=0
for s in "${SECRETS[@]}"; do
  "${COMPOSE[@]}" logs --no-color 2>/dev/null | grep -q "$s" && log_leaks=$((log_leaks + 1))
done
check "credential values found in any service's logs" "$log_leaks" 0
api -b "$A" -X POST -d '{"name":"Rate"}' "$BASE/api/v1/projects/$PA/endpoints" >/dev/null
RATE_HOOK="$(jq -r .url "$WORK/body")"
codes=""
for i in $(seq 1 40); do codes="$codes $(req -X POST -d x "$RATE_HOOK")"; done
accepted="$(tr ' ' '\n' <<<"$codes" | grep -c '^200$')"
limited="$(tr ' ' '\n' <<<"$codes" | grep -c '^429$')"
echo "      40 rapid requests to one endpoint: $accepted accepted, $limited limited"
if [ "$limited" -gt 0 ] && [ "$accepted" -ge 30 ] && [ "$accepted" -le 32 ]; then
  pass "per-endpoint burst of 30 enforced"
else
  fail "per-endpoint burst: $accepted accepted, $limited limited"
fi
req -D "$WORK/headers" -X POST -d x "$RATE_HOOK" >/dev/null
check "  429 carries Retry-After" "$(grep -ci '^retry-after:' "$WORK/headers")" 1

# --- 2.3 SSRF on the replay path (URL replay on) ------------------------------
section "2.3 SSRF protections on the replay path (REPLAY_ENABLED=true)"
"${REPLAY_ON[@]}" up -d --wait api worker >/dev/null 2>&1
api "$BASE/api/v1/config" >/dev/null
check "URL replay enabled for this section" "$(jq -r .replay_enabled "$WORK/body")" true
req -X POST -H 'Content-Type: application/json' -d '{"ssrf":"probe"}' "$HOOK/ssrf" >/dev/null
EV="$(jq -r .event_id "$WORK/body")"
replay_to() { # url -> "HTTP <status> <code>" or "<state> <failure_category>"
  local status
  status="$(api -b "$A" -X POST -H "Idempotency-Key: $(uuidgen)" \
    -d "$(jq -nc --arg u "$1" '{destination: {type: "url", url: $u}}')" "$BASE/api/v1/projects/$PA/events/$EV/replays")"
  if [ "$status" != 202 ] && [ "$status" != 201 ]; then
    echo "refused at creation: $status $(code)"
    return
  fi
  local id state
  id="$(jq -r .id "$WORK/body")"
  for _ in $(seq 1 60); do
    api -b "$A" "$BASE/api/v1/projects/$PA/replays/$id" >/dev/null
    state="$(jq -r .state "$WORK/body")"
    case "$state" in succeeded | failed) break ;; esac
    sleep 0.5
  done
  echo "accepted, then $state: $(jq -r '.failure_category // "none"' "$WORK/body")"
}
for url in \
  https://127.0.0.1/ https://127.1/ https://2130706433/ https://0x7f.0.0.1/ https://017700000001/ \
  https://[::1]/ https://[::ffff:127.0.0.1]/ https://[0:0:0:0:0:ffff:7f00:1]/ \
  https://10.0.0.1/ https://172.16.0.1/ https://192.168.1.1/ https://100.64.0.1/ https://[fc00::1]/ \
  https://169.254.169.254/latest/meta-data/ https://[fd00:ec2::254]/ https://metadata.google.internal/ \
  https://[fe80::1]/ https://224.0.0.1/ https://0.0.0.0/ https://255.255.255.255/ \
  https://localhost/ https://api:8080/ https://postgres/ https://caddy:8443/ https://localhost.:8443/ \
  https://localtest.me/ https://127.0.0.1.nip.io/ \
  http://example.com/ https://example.com:22/ https://user:pass@example.com/ "https://${BASE#https://}/h/x"; do
  result="$(replay_to "$url")"
  case "$result" in
    "accepted, then succeeded"*) fail "$url: $result" ;;
    "accepted, then failed: blocked"* | "accepted, then failed: dns"* | "refused at creation: 422 invalid_destination") pass "$url: $result" ;;
    *) fail "$url: $result" ;;
  esac
done
echo "      Redirects, DNS rebinding, the connect-time socket check, timeouts, and response caps"
echo "      need a controlled server; they are covered by the outbound package tests:"
(cd "$ROOT/backend" && go test -count=1 ./internal/replay/outbound/... -run 'Redirect|Rebind|Dial|Timeout|Limit|Size|Concurren' -v 2>&1 |
  grep -E '^(--- |ok|FAIL)' | sed 's/^/      /' | tail -40)
"${COMPOSE[@]}" up -d --wait api worker >/dev/null 2>&1
api "$BASE/api/v1/config" >/dev/null
check "URL replay off again (stack default)" "$(jq -r .replay_enabled "$WORK/body")" false

# --- 2.4 Production profile, database roles, and debug surface ------------------
section "2.4 Production profile, database roles, and debug surface"
startup() { # VAR=value -> the first error line when the api refuses to start
  # perl's alarm bounds the run: a configuration that is accepted would start the server.
  perl -e 'alarm 30; exec @ARGV' "${COMPOSE[@]}" run --rm --no-deps -T -e "$1" api /usr/local/bin/api 2>&1 | grep -m1 -iE 'invalid|must|refus|error' | cut -c1-160
}
for override in REPLAY_TEST_ALLOWLIST=echo OUTBOUND_ALLOW_HTTP=true APP_ORIGIN=http://localhost:8443 \
  ENDPOINT_TOKEN_HMAC_KEY=c2hvcnQ= "DATABASE_URL=postgres://app_runtime:x@pgbouncer:6432/relaylab?sslmode=disable" \
  RETENTION_EVENT_TTL=0; do
  line="$(startup "$override")"
  var="${override%%=*}"
  if [ -n "$line" ] && grep -q "$var" <<<"$line" && ! grep -q "${override#*=}" <<<"${line#*"$var"}"; then
    pass "refuses to start with $var unsafe; names the variable, not the value"
  else
    fail "$var unsafe: '$line'"
  fi
done
check "/debug/pprof/ is not served" "$(req "$BASE/debug/pprof/")" 404
check "/metrics is not public" "$(req "$BASE/metrics")" 404
check "Server header does not name versions" "$(curl -skI "$BASE/" | grep -ciE '^server:.*[0-9]')" 0
curl -skI "$BASE/" >"$WORK/h"
for h in Strict-Transport-Security Content-Security-Policy X-Content-Type-Options Referrer-Policy; do
  check "landing page sends $h" "$(grep -ci "^$h:" "$WORK/h")" 1
done
# Privilege checks read the catalog: nothing is modified.
priv() { sql "SELECT has_table_privilege('$1', '$2', '$3')"; }
colpriv() { sql "SELECT has_column_privilege('$1', '$2', '$3', '$4')"; }
r="$(as_role app_runtime prod-local-only-app-runtime 'CREATE TABLE app.x (id int)')"
check "app_runtime cannot create tables" "$(grep -c 'permission denied' <<<"$r")" 1
check "app_runtime cannot read password hashes (auth.account.password)" "$(colpriv app_runtime auth.account password SELECT)" f
check "app_runtime reads session tokens only to look sessions up (SELECT)" "$(colpriv app_runtime auth.session token SELECT)" t
for p in INSERT UPDATE DELETE; do
  check "app_runtime cannot $p auth.session" "$(priv app_runtime auth.session "$p")" f
done
check "app_runtime reads the migration version (readiness)" "$(priv app_runtime app.goose_db_version SELECT)" t
for p in INSERT UPDATE DELETE TRUNCATE; do
  check "app_runtime cannot $p migration history (app.goose_db_version)" "$(priv app_runtime app.goose_db_version "$p")" f
done
check "auth_runtime cannot read webhook events" "$(priv auth_runtime app.webhook_events SELECT)" f
r="$(as_role auth_runtime prod-local-only-auth-runtime 'CREATE TABLE auth.x (id int)')"
check "auth_runtime cannot create tables" "$(grep -c 'permission denied' <<<"$r")" 1

# --- 2.5 Retention and demo expiry ----------------------------------------------
section "2.5 Retention and demo expiry"
for i in $(seq 1 6); do req -X POST -d "r$i" "$HOOK/retention" >/dev/null; done
sql "UPDATE app.webhook_events SET received_at = now() - interval '8 days'
     WHERE id IN (SELECT id FROM app.webhook_events WHERE path = '/retention' AND project_id = '$PA' ORDER BY id LIMIT 3)" >/dev/null
before="$(sql "SELECT count(*) FILTER (WHERE received_at < now() - interval '7 days') || '/' || count(*) FROM app.webhook_events WHERE path = '/retention' AND project_id = '$PA'")"
echo "      /retention events (older than 7 days / all) before: $before"
"${COMPOSE[@]}" restart worker >/dev/null 2>&1
sleep 5
after="$(sql "SELECT count(*) FILTER (WHERE received_at < now() - interval '7 days') || '/' || count(*) FROM app.webhook_events WHERE path = '/retention' AND project_id = '$PA'")"
check "events older than RETENTION_EVENT_TTL deleted, newer kept (old/all after)" "$after" 0/3
req -c "$WORK/demo.jar" -X POST -H "Origin: $BASE" -H 'Content-Type: application/json' -d '{}' "$BASE/api/auth/sign-in/anonymous" >/dev/null
api -b "$WORK/demo.jar" "$BASE/api/v1/projects" >/dev/null
DEMO_USER="$(api -b "$WORK/demo.jar" "$BASE/api/v1/me" >/dev/null; jq -r .id "$WORK/body")"
counts() { sql "SELECT (SELECT count(*) FROM auth.\"user\" WHERE id = '$DEMO_USER') || '/' ||
  (SELECT count(*) FROM app.projects WHERE owner_id = '$DEMO_USER') || '/' ||
  (SELECT count(*) FROM app.webhook_events e JOIN app.projects p ON p.id = e.project_id WHERE p.owner_id = '$DEMO_USER')"; }
echo "      demo user/projects/events before: $(counts)"
sql "UPDATE auth.\"user\" SET \"createdAt\" = now() - interval '25 hours' WHERE id = '$DEMO_USER'" >/dev/null
"${COMPOSE[@]}" restart auth >/dev/null 2>&1
"${COMPOSE[@]}" up -d --wait auth >/dev/null 2>&1
sleep 2
check "expired demo workspace deleted with its data (user/projects/events)" "$(counts)" 0/0/0
check "real users untouched by the demo purge" "$(api -b "$B" "$BASE/api/v1/me")" 200

printf '\n%d passed, %d failed\n' "$passed" "$failed"
[ "$failed" = 0 ]
