#!/usr/bin/env bash
# Deploys RelayLab to Cloud Run from an operator's machine (see
# docs/deployment.md#deploying). Run it from a clean checkout of the commit to
# deploy, after `pnpm check` and the end-to-end suite pass on that commit.
#
#   deploy/deploy.sh            dry run: validate the manifests and build both
#                               images; nothing is pushed or deployed
#   deploy/deploy.sh --deploy   push the images, migrate, deploy the worker,
#                               deploy the app with no traffic, health-check
#                               it, then move all traffic to it
#
# Needs docker, gcloud (signed in as an account allowed to deploy), jq, yq,
# and the deploy-time variables of docs/deployment.md exported, plus
# GCP_PROJECT and ARTIFACT_REGISTRY (e.g. europe-north1-docker.pkg.dev/<project>/relaylab).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
DEPLOY=false
[ "${1:-}" = "--deploy" ] && DEPLOY=true

[ -z "$(git status --porcelain)" ] || { echo "the working tree has changes: deploy a clean commit" >&2; exit 1; }
TAG="sha-$(git rev-parse --short=12 HEAD)"
echo "Commit: $(git rev-parse HEAD) (tag $TAG)"

deploy/render.sh --check
docker build -t "relaylab-app:${TAG}" .
docker build -f auth/Dockerfile -t "relaylab-auth:${TAG}" .

if [ "$DEPLOY" != true ]; then
  echo "Dry run: images built and manifests validated. Nothing was pushed or deployed."
  exit 0
fi

: "${GCP_PROJECT:?}" "${REGION:?}" "${ARTIFACT_REGISTRY:?}" "${APP_ORIGIN:?}"
GCLOUD=(--project "$GCP_PROJECT" --region "$REGION")
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Push and pin by digest.
gcloud auth configure-docker "${ARTIFACT_REGISTRY%%/*}" --quiet
for name in app auth; do
  docker tag "relaylab-${name}:${TAG}" "${ARTIFACT_REGISTRY}/${name}:${TAG}"
  docker push "${ARTIFACT_REGISTRY}/${name}:${TAG}"
done
IMAGE_APP="$(docker inspect --format '{{index .RepoDigests 0}}' "${ARTIFACT_REGISTRY}/app:${TAG}")"
IMAGE_AUTH="$(docker inspect --format '{{index .RepoDigests 0}}' "${ARTIFACT_REGISTRY}/auth:${TAG}")"
export IMAGE_APP IMAGE_AUTH
deploy/render.sh "$WORK"

# Migrate: the auth schema first (app tables reference auth."user").
for job in migrate-auth migrate-app; do
  gcloud run jobs replace "$WORK/${job}-job.yaml" "${GCLOUD[@]}" --quiet
  gcloud run jobs execute "relaylab-${job}" "${GCLOUD[@]}" --wait
done

gcloud run services replace "$WORK/worker.yaml" "${GCLOUD[@]}" --quiet

# The new app revision gets only a tag URL; traffic stays where it is.
current="$(gcloud run services describe relaylab-app "${GCLOUD[@]}" \
  --format 'value(status.traffic[0].revisionName)' 2>/dev/null || true)"
if [ -n "$current" ]; then
  CURRENT="$current" TAG="$TAG" yq -i '.spec.traffic = [{"revisionName": strenv(CURRENT), "percent": 100},
    {"latestRevision": true, "percent": 0, "tag": strenv(TAG)}]' "$WORK/app.yaml"
else
  echo "First deploy: no previous revision to keep traffic on."
  TAG="$TAG" yq -i '.spec.traffic = [{"latestRevision": true, "percent": 100, "tag": strenv(TAG)}]' "$WORK/app.yaml"
fi
gcloud run services replace "$WORK/app.yaml" "${GCLOUD[@]}" --quiet

url="$(gcloud run services describe relaylab-app "${GCLOUD[@]}" --format json |
  jq -r --arg tag "$TAG" '.status.traffic[] | select(.tag == $tag) | .url')"
[ -n "$url" ] || { echo "no URL for the tagged revision" >&2; exit 1; }
for path in /healthz /readyz; do
  curl --fail --silent --show-error --retry 10 --retry-delay 6 --retry-all-errors --max-time 10 "${url}${path}"
  echo
done

gcloud run services update-traffic relaylab-app "${GCLOUD[@]}" --to-tags "${TAG}=100"
curl --fail --silent --show-error --max-time 10 "${APP_ORIGIN}/readyz"
echo
echo "Deployed $TAG. Roll back with: gcloud run services update-traffic relaylab-app ${GCLOUD[*]} --to-revisions <previous>=100"
