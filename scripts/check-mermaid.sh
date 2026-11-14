#!/usr/bin/env bash
# Renders every Mermaid diagram in README.md and docs/ with mermaid-cli
# (Docker), so a syntax error fails here instead of on GitHub.
#
#   scripts/check-mermaid.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="minlag/mermaid-cli:11.4.2@sha256:99c983b3ab4e14033f2880bc1b9de17e5090b4515dabd63fe9cf8c0ae6130956"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
chmod 777 "$WORK"

count=0
while IFS= read -r file; do
  awk -v out="$WORK" -v base="$(echo "${file#"$ROOT"/}" | tr '/' '_')" '
    /^```mermaid[[:space:]]*$/ { inblock = 1; n++; f = sprintf("%s/%s_%02d.mmd", out, base, n); next }
    inblock && /^```[[:space:]]*$/ { inblock = 0; close(f); next }
    inblock { print > f }
  ' "$file"
done < <(find "$ROOT/README.md" "$ROOT/docs" -name '*.md' -type f | sort)

failed=0
for f in "$WORK"/*.mmd; do
  [ -e "$f" ] || continue
  count=$((count + 1))
  name="$(basename "$f")"
  if ! docker run --rm -v "$WORK:/data" "$IMAGE" -q -i "/data/$name" -o "/data/${name%.mmd}.svg" > "$WORK/${name%.mmd}.log" 2>&1; then
    echo "FAIL  $name" >&2
    sed 's/^/      /' "$WORK/${name%.mmd}.log" >&2
    failed=1
  else
    echo "  ok  $name"
  fi
done
[ "$count" -gt 0 ] || { echo "no Mermaid diagrams found" >&2; exit 1; }
[ "$failed" = 0 ] || exit 1
echo "All $count diagrams render."
