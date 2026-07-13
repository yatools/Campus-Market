import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    reads: { executor: 'constant-vus', exec: 'readFlow', vus: 80, duration: '60s' },
    writes: { executor: 'constant-vus', exec: 'writeFlow', vus: 20, duration: '60s' },
  },
  thresholds: {
    'http_req_failed{kind:read}': ['rate<0.01'],
    'http_req_failed{kind:write}': ['rate<0.01'],
    'http_req_duration{kind:read}': ['p(95)<500'],
    'http_req_duration{kind:write}': ['p(95)<800'],
  },
};

const base = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const session = __ENV.SESSION_COOKIE;
const csrf = __ENV.CSRF_TOKEN;

export function readFlow() {
  const path = ['/api/v1/posts?page_size=20', '/api/v1/teams?page_size=20', '/api/v1/hot'][__ITER % 3];
  const response = http.get(`${base}${path}`, { tags: { kind: 'read' } });
  check(response, { 'read status is 200': (result) => result.status === 200 });
  sleep(0.2);
}

export function writeFlow() {
  if (!session || !csrf) throw new Error('SESSION_COOKIE and CSRF_TOKEN are required for write load');
  const response = http.post(
    `${base}/api/v1/posts`,
    JSON.stringify({
      title: `load-${__VU}-${__ITER}`,
      body: `staging load test ${__VU}-${__ITER}`,
      identity_mode: 'nickname',
      visibility: '24h',
      allow_comments: false,
    }),
    {
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrf,
        Cookie: `wutong_session=${session}; wutong_csrf=${csrf}`,
      },
      tags: { kind: 'write' },
    },
  );
  check(response, { 'write status is 201': (result) => result.status === 201 });
  sleep(0.5);
}

export default readFlow;
