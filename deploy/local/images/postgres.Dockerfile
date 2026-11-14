# Local stacks only (see README.md).
FROM postgres:17-alpine@sha256:d3244e8ce3f165ee3c93d9c4a4b1d0fe48b3a89e2287b717fdcbbde92d405065
# The entrypoint drops root with gosu, a Go binary built by upstream with an
# old Go release (it carries that release's stdlib CVEs). su-exec does the
# same job with the same arguments and is maintained by Alpine.
RUN apk upgrade --no-cache && apk add --no-cache su-exec \
 && rm /usr/local/bin/gosu \
 && sed -i 's/exec gosu postgres/exec su-exec postgres/' /usr/local/bin/docker-entrypoint.sh \
 && grep -q 'exec su-exec postgres' /usr/local/bin/docker-entrypoint.sh
# The official entrypoint starts as root only to prepare the data directory,
# then runs the server as the postgres user (accepted in .trivyignore.yaml).
