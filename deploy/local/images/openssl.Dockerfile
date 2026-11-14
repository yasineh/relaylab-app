# Production-mode local stack only (see README.md): generates its throwaway CA
# and server certificate into the certs volume.
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk upgrade --no-cache && apk add --no-cache openssl && mkdir /certs && chown 70:70 /certs
# Postgres and PgBouncer run as uid 70 and need to own the key. A new named
# volume takes /certs's ownership, so running as uid 70 creates the files
# with the right owner, without root.
USER 70:70
