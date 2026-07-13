import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    public_reads: {
      executor: 'constant-vus',
      vus: 100,
      duration: '60s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

const base = __ENV.BASE_URL || 'http://127.0.0.1:8000';

export default function () {
  for (const path of ['/api/v1/posts?page_size=20', '/api/v1/teams?page_size=20', '/api/v1/hot']) {
    const response = http.get(`${base}${path}`);
    check(response, { 'status is 200': (r) => r.status === 200 });
  }
  sleep(1);
}
