# Production-mode local stack only (see README.md).
FROM caddy:2.11-alpine@sha256:003a97808e781aa9f7e75cc2f43d34d461099031faf5e20faefb9f61126836f0
RUN apk upgrade --no-cache \
 && addgroup -S caddy && adduser -S -G caddy -H caddy \
 && chown -R caddy:caddy /data /config
# Listens on 8443 (unprivileged), so it does not need root.
USER caddy
