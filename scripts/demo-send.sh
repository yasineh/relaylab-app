#!/usr/bin/env bash
# Sends the synthetic demo webhook used in the product walkthrough
# (docs/demo/walkthrough.md) to a RelayLab receiving URL.
#
#   scripts/demo-send.sh [-k] <endpoint-url>
#
#   -k   accept an untrusted TLS certificate (the production-mode local
#        stack at https://localhost:8443 uses a local CA)
#
# Everything sent is synthetic: reserved example.com domains, demo_-prefixed
# fake identifiers, and a fake bearer token that RelayLab removes on arrival.
set -euo pipefail

insecure=()
if [[ "${1:-}" == "-k" ]]; then
  insecure=(--insecure)
  shift
fi
if [[ $# -ne 1 || "$1" != http*://*/h/* ]]; then
  echo "usage: $0 [-k] <endpoint-url>   (the receiving URL, ending in /h/<token>)" >&2
  exit 2
fi
url="${1%/}"

body='{"id":"evt_demo_shipment_1","type":"order.shipped","created":"2026-11-14T09:30:00Z","data":{"order_id":"demo-2048","carrier":"ExampleParcel","tracking":"DEMO123456789","items":[{"sku":"MUG-01","name":"Coffee mug","qty":2},{"sku":"TEE-L","name":"T-shirt (L)","qty":1}],"customer":{"id":"cus_demo_7","email":"customer@example.com"}}}'

curl --silent --show-error --fail-with-body "${insecure[@]}" \
  --request POST \
  --header 'Content-Type: application/json' \
  --header 'User-Agent: ExampleShop-Webhooks/1.0' \
  --header 'X-Example-Event: order.shipped' \
  --header 'X-Example-Delivery: demo_delivery_0042' \
  --header 'Authorization: Bearer demo_token_not_a_real_secret' \
  --data "$body" \
  --write-out '\nHTTP %{http_code}\n' \
  "$url/orders/shipped?attempt=1&store=demo"
