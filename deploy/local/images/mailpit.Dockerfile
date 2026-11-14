# Local stacks only (see README.md): catches verification and reset emails.
FROM axllent/mailpit:v1.33@sha256:aef2aacc11f075ec0fb6874275ee4a421e688e11a96ebb296134d1735c7761ce
RUN apk upgrade --no-cache && addgroup -S mailpit && adduser -S -G mailpit -H mailpit
# Listens on 1025 and 8025 (unprivileged) and keeps mail in memory.
USER mailpit
