import http from 'k6/http';
import { check } from 'k6';

const rate = Number(__ENV.RATE || 100);
const duration = __ENV.DURATION || '30s';
const baseURL = __ENV.BASE_URL || 'http://feed-api:8080';

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  scenarios: {
    feed_arrival_rate: {
      executor: 'constant-arrival-rate',
      rate,
      timeUnit: '1s',
      duration,
      preAllocatedVUs: Math.max(20, Math.ceil(rate / 5)),
      maxVUs: Math.max(100, rate * 2),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<250', 'p(99)<750'],
    checks: ['rate>0.99'],
  },
};

export default function () {
  const userID = (__VU % 2) + 1;
  const response = http.get(`${baseURL}/v1/feed?user_id=${userID}`, {
    tags: { name: 'GET /v1/feed' },
  });
  check(response, {
    'feed status is 200': (result) => result.status === 200,
    'feed response is JSON': (result) => result.headers['Content-Type']?.includes('application/json'),
  });
}
