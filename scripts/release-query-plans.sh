#!/usr/bin/env bash
# Query-plan review (release-hardening-review tasks 1.2 and 2.6) on the
# production-mode local stack: seeds a real account's project with 10,000
# synthetic events and 1,000 replays (plus a second project of the same size,
# so indexes must select by project), then runs EXPLAIN ANALYZE for the event
# list, event detail, and replay history queries as the Go runtime role.
#
#   docker compose -f docker-compose.prod.yml up -d --build --wait
#   scripts/release-query-plans.sh | tee docs/release/evidence/query-plans-<date>.log
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="${PROD_BASE_URL:-https://localhost:${PROD_HTTPS_PORT:-8443}}"
COMPOSE=(docker compose -f "$ROOT/docker-compose.prod.yml")
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
sql() { "${COMPOSE[@]}" exec -T postgres psql -U postgres -d relaylab -Atc "$1"; }
runtime() {
  "${COMPOSE[@]}" exec -T -e PGPASSWORD=prod-local-only-app-runtime postgres \
    psql -h 127.0.0.1 -U app_runtime -d relaylab -X -q -c "$1"
}
api() { curl -sk -o "$WORK/body" -w '%{http_code}' -H "Origin: $BASE" -H 'Content-Type: application/json' "$@"; }

echo "# Query plans on 10,000 events"
echo "Commit: $(git -C "$ROOT" rev-parse --short HEAD)  Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

email="plans-$$@example.test"
password="plans-cobalt-$$-$RANDOM"
printf '%s\n' "$password" | "${COMPOSE[@]}" exec -T auth node src/cli/create-user.ts --email "$email" --name "Plans" >/dev/null
api -c "$WORK/jar" -X POST -d "$(jq -nc --arg e "$email" --arg p "$password" '{email: $e, password: $p}')" "$BASE/api/auth/sign-in/email" >/dev/null
OWNER="$(api -b "$WORK/jar" "$BASE/api/v1/me" >/dev/null; jq -r .id "$WORK/body")"
projects=()
for name in "Plans" "Plans neighbour"; do
  api -b "$WORK/jar" -X POST -d "{\"name\":\"$name\"}" "$BASE/api/v1/projects" >/dev/null
  pid="$(jq -r .id "$WORK/body")"
  api -b "$WORK/jar" -X POST -d '{"name":"Inbound"}' "$BASE/api/v1/projects/$pid/endpoints" >/dev/null
  eid="$(jq -r .id "$WORK/body")"
  projects+=("$pid:$eid")
done
PROJECT="${projects[0]%%:*}"
ENDPOINT="${projects[0]##*:}"

# Time-ordered (UUIDv7-shaped) ids, received over the last 7 days, mixed methods
# and content types, like the ingestion path writes them.
for pe in "${projects[@]}"; do
  sql "INSERT INTO app.webhook_events (id, endpoint_id, project_id, received_at, method, path, raw_query,
         query_params, headers, content_type, body, body_size, body_encoding, body_json, correlation_id)
       SELECT (lpad(to_hex((extract(epoch FROM t) * 1000)::bigint), 12, '0') || '7' ||
               substr(md5(random()::text), 1, 3) || '8' || substr(md5(random()::text), 1, 15))::uuid,
              '${pe##*:}', '${pe%%:*}', t,
              (ARRAY['POST','POST','POST','GET','PUT','DELETE'])[1 + (g % 6)], '/orders/' || g, '',
              '{}'::jsonb, '[[\"Content-Type\",\"application/json\"]]'::jsonb,
              (ARRAY['application/json','application/json','text/plain','application/x-www-form-urlencoded'])[1 + (g % 4)],
              convert_to('{\"n\":' || g || '}', 'UTF8'), length('{\"n\":' || g || '}'), 'utf8',
              ('{\"n\":' || g || '}')::jsonb, 'plans-' || g
       FROM generate_series(1, 10000) g,
            LATERAL (SELECT now() - (g * interval '60 seconds')) AS ts(t)" >/dev/null
done
sql "INSERT INTO app.replay_attempts (id, project_id, event_id, actor_user_id, destination_type, sink_endpoint_id,
       method, state, outcome, tries, response_status, created_at, started_at, finished_at, duration_ms)
     SELECT gen_random_uuid(), '$PROJECT', e.id, '$OWNER', 'sink', '$ENDPOINT', 'POST', 'succeeded', 'delivered', 1,
            200, e.received_at + interval '5 seconds', e.received_at + interval '5 seconds',
            e.received_at + interval '5 seconds', 3
     FROM (SELECT id, received_at FROM app.webhook_events WHERE project_id = '$PROJECT' ORDER BY id DESC LIMIT 1000) e" >/dev/null
sql "ANALYZE app.webhook_events; ANALYZE app.replay_attempts" >/dev/null
echo "Seeded: $(sql "SELECT count(*) FROM app.webhook_events WHERE project_id = '$PROJECT'") events and" \
  "$(sql "SELECT count(*) FROM app.replay_attempts WHERE project_id = '$PROJECT'") replays in the review project;" \
  "$(sql "SELECT count(*) FROM app.webhook_events") events in all projects"
EVENT="$(sql "SELECT id FROM app.webhook_events WHERE project_id = '$PROJECT' ORDER BY id DESC OFFSET 5000 LIMIT 1")"

plan() { # title, query
  printf '\n## %s\n\n```\n' "$1"
  runtime "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, TIMING ON) $2"
  printf '```\n'
}
LIST_WHERE="e.project_id = '$PROJECT' AND p.owner_id = '$OWNER'
  AND e.id > '00000000-0000-0000-0000-000000000000' AND e.id < 'ffffffff-ffff-ffff-ffff-ffffffffffff'"
LIST="SELECT e.id, e.endpoint_id, en.name, e.received_at, e.method, e.path, e.content_type, e.body_size
  FROM app.webhook_events e JOIN app.projects p ON p.id = e.project_id JOIN app.endpoints en ON en.id = e.endpoint_id"

plan "Event list, first page (ListEventsDesc)" \
  "$LIST WHERE $LIST_WHERE ORDER BY e.id DESC LIMIT 51"
plan "Event list, filtered by method and content type (ListEventsDesc)" \
  "$LIST WHERE $LIST_WHERE AND e.method = ANY('{PUT}'::text[]) AND e.content_type ILIKE 'text/%' ESCAPE '\\' ORDER BY e.id DESC LIMIT 51"
plan "Event list, oldest first (ListEventsAsc)" \
  "$LIST WHERE $LIST_WHERE ORDER BY e.id ASC LIMIT 51"
plan "Event detail (GetEvent)" \
  "SELECT e.*, en.name FROM app.webhook_events e JOIN app.projects p ON p.id = e.project_id
   JOIN app.endpoints en ON en.id = e.endpoint_id
   WHERE e.id = '$EVENT' AND e.project_id = '$PROJECT' AND p.owner_id = '$OWNER'"
plan "Replay history, first page (ListReplays)" \
  "SELECT r.id, r.event_id, r.state, r.response_status, r.created_at FROM app.replay_attempts r
   JOIN app.projects p ON p.id = r.project_id LEFT JOIN app.endpoints en ON en.id = r.sink_endpoint_id
   WHERE r.project_id = '$PROJECT' AND p.owner_id = '$OWNER' ORDER BY r.created_at DESC, r.id DESC LIMIT 51"
plan "Replays of one event (ListReplays with event_id)" \
  "SELECT r.id, r.state, r.created_at FROM app.replay_attempts r JOIN app.projects p ON p.id = r.project_id
   WHERE r.project_id = '$PROJECT' AND p.owner_id = '$OWNER' AND r.event_id = '$EVENT'
   ORDER BY r.created_at DESC, r.id DESC LIMIT 51"
