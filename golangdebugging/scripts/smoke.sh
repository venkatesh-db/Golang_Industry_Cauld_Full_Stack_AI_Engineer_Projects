#!/bin/sh
set -eu

base_url="${BASE_URL:-http://localhost:18080}"
diagnostics_token="${DIAGNOSTICS_TOKEN:-local-diagnostics-token}"
failure_token="${FAILURE_LAB_TOKEN:-local-failure-lab-token}"

health_status="$(curl -sS -o /dev/null -w '%{http_code}' "$base_url/healthz")"
test "$health_status" = "204"

feed_status="$(curl -sS -o /dev/null -w '%{http_code}' "$base_url/v1/feed?user_id=1")"
test "$feed_status" = "200"

curl -fsS "$base_url/metrics" | grep -q 'http_requests_total'
curl -fsS "$base_url/internal/diagnostics/snapshot" \
  -H "X-Diagnostics-Token: $diagnostics_token" | grep -q '"runtime"'
curl -fsS "$base_url/internal/failure-lab/status" \
  -H "X-Failure-Lab-Token: $failure_token" | grep -q '"leaked_goroutines"'

printf 'smoke test passed: health=204 feed=200 metrics=ok diagnostics=ok failure-lab=ok\n'
