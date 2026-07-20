#!/usr/bin/env bash
# Vegeta fixed-rate benchmark against edge-api /feed — a second, independent data
# point next to k6 (different tool, same SLO lens). Writes an HDR-style report.
set -euo pipefail

RATE="${RATE:-200}"          # requests/sec
DURATION="${DURATION:-60s}"
OUT="${OUT:-loadtest/results/vegeta-feed.txt}"

# Random-ish user ids via a target-generating loop.
gen_targets() {
  for i in $(seq 1 500); do
    echo "GET http://localhost:8080/feed/$(( (RANDOM % 2000) + 1 ))?limit=20"
  done
}

echo "vegeta: ${RATE}/s for ${DURATION}"
gen_targets | vegeta attack -rate="${RATE}" -duration="${DURATION}" \
  | tee results.bin \
  | vegeta report -type=text | tee "${OUT}"

echo "latency histogram:"
vegeta report -type='hist[0,5ms,10ms,25ms,50ms,100ms,250ms,500ms,1s]' < results.bin | tee -a "${OUT}"
rm -f results.bin
echo "wrote ${OUT}"
