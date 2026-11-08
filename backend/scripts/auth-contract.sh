#!/bin/sh
# Contract and auth-schema integration tests: the real auth service (auth/) behind
# the real Go proxy and session validator, on a real PostgreSQL.
#
# CI (auth-contract job) passes CI_ADMIN_DATABASE_URL; locally the isolated
# postgres-test container is used. Needs Node.js and `pnpm install`.
set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
if [ -n "${CI_ADMIN_DATABASE_URL:-}" ]; then
  export RELAYLAB_TEST_DATABASE_URL="$CI_ADMIN_DATABASE_URL"
elif [ -z "${RELAYLAB_TEST_DATABASE_URL:-}" ]; then
  PORT="${POSTGRES_TEST_HOST_PORT:-54321}"
  docker compose -f "$ROOT/docker-compose.yml" --profile test up -d --wait postgres-test >&2
  export RELAYLAB_TEST_DATABASE_URL="postgres://postgres:test-only-postgres@localhost:${PORT}/postgres?sslmode=disable"
fi
export RELAYLAB_REQUIRE_DB=1
export RELAYLAB_REQUIRE_AUTH=1
cd "$ROOT/backend"
exec go test -race -count=1 -run 'Contract|AuthDB' ./...
