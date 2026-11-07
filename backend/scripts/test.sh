#!/bin/sh
# Go unit + integration tests against the isolated postgres-test container.
# Postgres roles are cluster-wide, so tests never use the dev database server.
set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PORT="${POSTGRES_TEST_HOST_PORT:-54321}"

docker compose -f "$ROOT/docker-compose.yml" --profile test up -d --wait postgres-test

export RELAYLAB_REQUIRE_DB=1
export RELAYLAB_TEST_DATABASE_URL="${RELAYLAB_TEST_DATABASE_URL:-postgres://postgres:test-only-postgres@localhost:${PORT}/postgres?sslmode=disable}"
exec go test -race -count=1 ./...
