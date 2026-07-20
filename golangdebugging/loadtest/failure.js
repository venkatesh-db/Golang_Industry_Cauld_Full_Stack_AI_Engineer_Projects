import http from 'k6/http';
import { check, sleep } from 'k6';

const baseURL = __ENV.BASE_URL || 'http://feed-api:8080';
const recommendationURL = __ENV.RECOMMENDATION_URL || 'http://recommendation-service:8081';
const token = __ENV.FAILURE_LAB_TOKEN || 'local-failure-lab-token';
const headers = { 'X-Failure-Lab-Token': token };

export const options = {
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  scenarios: {
    feed_during_pressure: {
      executor: 'constant-vus',
      vus: 20,
      duration: '30s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.10'],
    http_req_duration: ['p(99)<1500'],
  },
};

export function setup() {
  http.post(`${baseURL}/internal/failure-lab/goroutine-leak?count=100`, null, { headers });
  http.post(`${baseURL}/internal/failure-lab/memory-pressure?mib=64`, null, { headers });
  http.post(`${baseURL}/internal/failure-lab/db-pool-exhaustion?connections=10&duration_ms=15000`, null, { headers });
  http.post(`${recommendationURL}/internal/failure-lab/slow-dependency?duration_ms=1000`, null, { headers });
}

export default function () {
  const response = http.get(`${baseURL}/v1/feed?user_id=${(__VU % 2) + 1}`, {
    tags: { name: 'GET /v1/feed under failure pressure' },
  });
  check(response, {
    'feed returns a controlled response': (result) => result.status === 200 || result.status === 503,
  });
  sleep(0.1);
}

export function teardown() {
  http.post(`${baseURL}/internal/failure-lab/reset`, null, { headers });
  http.post(`${recommendationURL}/internal/failure-lab/reset`, null, { headers });
}
