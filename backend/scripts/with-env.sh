#!/bin/sh
# Runs a command with the repository-root .env exported (local development only).
# Production configuration comes from the deployment environment, never a file.
set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
if [ -f "$ROOT/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$ROOT/.env"
  set +a
fi
exec "$@"
