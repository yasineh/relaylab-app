#!/bin/sh
# Auth service unit + integration tests against the isolated postgres-test container.
# Postgres roles are cluster-wide, so tests never use the development database server.
# CI provides RELAYLAB_TEST_DATABASE_URL itself and has no Docker Compose stack.
set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
if [ -z "${RELAYLAB_TEST_DATABASE_URL:-}" ]; then
  PORT="${POSTGRES_TEST_HOST_PORT:-54321}"
  docker compose -f "$ROOT/docker-compose.yml" --profile test up -d --wait postgres-test >&2
  export RELAYLAB_TEST_DATABASE_URL="postgres://postgres:test-only-postgres@localhost:${PORT}/postgres?sslmode=disable"
fi
PATH="$(cd "$(dirname "$0")/.." && pwd)/node_modules/.bin:$PATH" exec vitest run "$@"
