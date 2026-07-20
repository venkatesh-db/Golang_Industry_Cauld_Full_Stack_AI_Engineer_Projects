// Ramp/spike profile: pushes edge-api past comfort to watch p99 + saturation and
// (optionally, with chaos armed) the breaker open.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 500,
      stages: [
        { target: 100, duration: '30s' },
        { target: 800, duration: '45s' }, // spike
        { target: 100, duration: '30s' },
      ],
    },
  },
};

export default function () {
  const uid = Math.floor(Math.random() * 2000) + 1;
  const res = http.get(`http://localhost:8080/feed/${uid}?limit=20`);
  check(res, { 'not 5xx': (r) => r.status < 500 });
}
