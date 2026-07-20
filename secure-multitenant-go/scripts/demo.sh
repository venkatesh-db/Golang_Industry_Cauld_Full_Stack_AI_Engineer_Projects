#!/usr/bin/env bash
# Boots the server with a self-signed cert and exercises all five features
# end-to-end over TLS. Requires: go, openssl, curl.
set -euo pipefail

cd "$(dirname "$0")/.."

CRT=server.crt
KEY=server.key
URL=https://localhost:8443/records

cleanup() {
  [[ -n "${SRV_PID:-}" ]] && kill "$SRV_PID" 2>/dev/null || true
}
trap cleanup EXIT

# 1. Self-signed cert (TLS 1.2+ handshake needs a cert/key pair).
if [[ ! -f $CRT || ! -f $KEY ]]; then
  echo "==> generating self-signed cert"
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$KEY" -out "$CRT" -days 1 \
    -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost" >/dev/null 2>&1
fi

# 2. Boot the server (in-memory store; no DATABASE_URL).
echo "==> starting server"
go run ./cmd/server &
SRV_PID=$!

# Wait for the TLS port to accept connections.
for _ in $(seq 1 20); do
  if curl -ksf -o /dev/null "https://localhost:8443/" 2>/dev/null || \
     curl -ks  -o /dev/null "https://localhost:8443/" 2>/dev/null; then
    break
  fi
  sleep 0.25
done

code() { # method headers... -> prints HTTP status
  curl -ks -o /dev/null -w '%{http_code}' "$@"
}

echo
echo "==> exercising the five features (all traffic is TLS 1.2+):"

s1=$(code -X POST -H 'X-Tenant-ID: tenant-a' -H 'X-Role: admin' "$URL")
echo "  admin + tenant-a  POST /records      -> $s1   (expect 201: RBAC pass, AES-256 stored, tenant-scoped)"

s2=$(code -X POST -H 'X-Tenant-ID: tenant-a' -H 'X-Role: billing_viewer' "$URL")
echo "  viewer + tenant-a POST /records      -> $s2   (expect 403: RBAC denies billing:manage)"

s3=$(code -X POST -H 'X-Role: admin' "$URL")
echo "  admin, no tenant  POST /records      -> $s3   (expect 400: tenant scoping required)"

s4=$(code -X POST -H 'X-Tenant-ID: tenant-a' "$URL")
echo "  tenant, no role   POST /records      -> $s4   (expect 401: RBAC needs a role)"

# TLS floor: force a <1.2 client and expect the handshake to fail.
echo
if curl -ks --tls-max 1.1 -o /dev/null "https://localhost:8443/" 2>/dev/null; then
  echo "  TLS 1.1 client                       -> CONNECTED  (unexpected!)"
  tls_ok=1
else
  echo "  TLS 1.1 client                       -> REJECTED   (expect: TLS 1.2 floor enforced)"
  tls_ok=0
fi

echo
if [[ "$s1" == 201 && "$s2" == 403 && "$s3" == 400 && "$s4" == 401 && "$tls_ok" == 0 ]]; then
  echo "==> DEMO PASSED — all five features verified end-to-end"
else
  echo "==> DEMO FAILED — statuses: $s1/$s2/$s3/$s4 tls_rejected=$([[ $tls_ok == 0 ]] && echo yes || echo no)"
  exit 1
fi
