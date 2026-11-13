#!/bin/sh
# Production-mode local stack only: a throwaway CA and one server certificate
# (postgres, pgbouncer) so every database connection uses verified TLS, as in
# production. Runs once in the certs container; output goes to /certs.
set -eu
cd /certs
if [ -f server.crt ]; then
  echo "certificates exist"
  exit 0
fi
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=RelayLab local prod CA" \
  -keyout ca.key -out ca.crt
openssl req -newkey rsa:2048 -nodes -subj "/CN=postgres" -keyout server.key -out server.csr
printf 'subjectAltName=DNS:postgres,DNS:pgbouncer\nextendedKeyUsage=serverAuth\n' > ext.cnf
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 30 -extfile ext.cnf -out server.crt
rm -f server.csr ext.cnf ca.srl ca.key
# Postgres and PgBouncer run as uid 70 and refuse a key others can read.
chown 70:70 server.key server.crt
chmod 600 server.key
chmod 644 server.crt ca.crt
echo "certificates created"
