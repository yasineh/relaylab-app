#!/bin/sh
# Renders the Cloud Run definitions in deploy/cloudrun/ for one deployment.
#
#   deploy/render.sh <out-dir>   substitute ${VARS} from the environment into
#                                <out-dir>/*.yaml; fails if any is unset
#   deploy/render.sh --check     render with placeholder values and validate
#                                every manifest offline (no cloud calls, no
#                                credentials): see backend/internal/deploycheck
#
# Only the ${NAME} variables the manifests use are substituted, so other "$"
# characters are left alone. Variables are deploy-time values (region, image
# references, origin, service accounts); secrets are never variables here,
# they are Secret Manager references in the manifests.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/deploy/cloudrun"

if [ "${1:-}" = "--check" ]; then
  cd "$ROOT/backend"
  exec go test -count=1 ./internal/deploycheck/
fi

OUT="${1:?usage: deploy/render.sh <out-dir> | --check}"
command -v envsubst >/dev/null || { echo "envsubst (gettext) is required" >&2; exit 1; }
VARS="$(grep -ho '\${[A-Z_][A-Z0-9_]*}' "$SRC"/*.yaml | sort -u)"
missing=""
for v in $VARS; do
  name="$(echo "$v" | tr -d '${}')"
  eval "set_=\${$name+x}"
  [ -n "$set_" ] || missing="$missing $name"
done
if [ -n "$missing" ]; then
  echo "unset variables:$missing" >&2
  exit 1
fi
mkdir -p "$OUT"
for f in "$SRC"/*.yaml; do
  envsubst "$(echo $VARS)" < "$f" > "$OUT/$(basename "$f")"
done
echo "rendered $(ls "$SRC"/*.yaml | wc -l | tr -d ' ') manifests to $OUT"
