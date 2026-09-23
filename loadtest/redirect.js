// Load test for the redirect hot path — the endpoint the "100M
// requests/day" claim is actually about. Run:
//   BASE_URL=http://localhost:8080 CODE=<existing code> k6 run loadtest/redirect.js
import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const CODE = __ENV.CODE;

export const options = {
  scenarios: {
    redirect_throughput: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: 50 },
        { duration: '30s', target: 200 },
        { duration: '20s', target: 200 },
        { duration: '10s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.001'],
    http_req_duration: ['p(99)<50'],
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/${CODE}`, { redirects: 0 });
  check(res, { 'status is 302': (r) => r.status === 302 });
}
