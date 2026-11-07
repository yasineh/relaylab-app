#!/bin/sh
# Local development only: runs once when the Postgres container initialises an
# empty data directory. Applies deploy/sql/roles.sql (mounted at /deploy-sql)
# with role passwords taken from the container environment (.env placeholders).
set -eu

: "${APP_MIGRATOR_PASSWORD:?}" "${APP_RUNTIME_PASSWORD:?}" "${AUTH_MIGRATOR_PASSWORD:?}" "${AUTH_RUNTIME_PASSWORD:?}"

PGOPTIONS="-c relaylab.app_migrator_password=${APP_MIGRATOR_PASSWORD} \
-c relaylab.app_runtime_password=${APP_RUNTIME_PASSWORD} \
-c relaylab.auth_migrator_password=${AUTH_MIGRATOR_PASSWORD} \
-c relaylab.auth_runtime_password=${AUTH_RUNTIME_PASSWORD}" \
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /deploy-sql/roles.sql
