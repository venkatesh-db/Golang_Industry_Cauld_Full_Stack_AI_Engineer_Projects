// Steady-RPS baseline against edge-api /feed. Establishes p50/p95/p99 under
// nominal load — the numbers you commit to loadtest/results/.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    baseline: {
      executor: 'constant-arrival-rate',
      rate: 200,            // 200 req/s
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'], // mirrors the SLO
    http_req_failed: ['rate<0.01'],
  },
};

export default function () {
  const uid = Math.floor(Math.random() * 2000) + 1;
  const res = http.get(`http://localhost:8080/feed/${uid}?limit=20`);
  check(res, { 'status 200': (r) => r.status === 200 });
}
