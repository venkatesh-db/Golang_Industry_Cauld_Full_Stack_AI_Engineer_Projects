// Write-path load: hammers counter-service likes (Redis incr + Postgres upsert).
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    writes: {
      executor: 'constant-arrival-rate',
      rate: 100,
      timeUnit: '1s',
      duration: '1m',
      preAllocatedVUs: 40,
      maxVUs: 150,
    },
  },
  thresholds: { http_req_duration: ['p(95)<300'] },
};

export default function () {
  const uid = Math.floor(Math.random() * 2000) + 1;
  const res = http.post(`http://localhost:8082/counts/${uid}/like`);
  check(res, { 'status 200': (r) => r.status === 200 });
}
