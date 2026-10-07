/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check } from "loadtool";

// Token authentication with setup and teardown: log in once, then send
// the token as a bearer header from every VU.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/auth-token.ts --vus 10 --duration 10s
//
// Order: top-level code (once per VU) -> setup() -> load phase ->
// teardown(). Requests and checks in setup and teardown are not counted
// in the results.
//
// Pass real credentials through the environment, never in the script:
//   loadtool run -e API_USER=... -e API_PASSWORD=... examples/auth-token.ts

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

interface Data {
  token: string;
  username: string;
}

// Runs once. Throwing here stops the test before any load is generated,
// so a target that is down or a wrong password fails fast instead of
// producing a wall of errors. The return value is passed (as JSON) to
// every iteration and to teardown.
export function setup(): Data {
  const health = http.get(`${BASE_URL}/health`);
  if (health.status !== 200) {
    throw new Error(`target is not healthy: status ${health.status} ${health.error}`);
  }
  const username = __ENV.API_USER || "load-test";
  const login = http.post(
    `${BASE_URL}/api/login`,
    JSON.stringify({ username, password: __ENV.API_PASSWORD || "demo" }),
    { headers: { "Content-Type": "application/json" } },
  );
  if (login.status !== 200) {
    throw new Error(`login failed: status ${login.status} ${login.body}`);
  }
  return { token: login.json().token, username };
}

// Runs repeatedly in every VU, with this VU's own copy of the setup data.
export default function (data: Data): void {
  const res = http.get(`${BASE_URL}/api/me`, {
    headers: { Authorization: `Bearer ${data.token}` },
  });
  check(res, {
    "me: status is 200": (r) => r.status === 200,
    "me: right user": (r) => r.json().username === data.username,
  });
}

// Runs once after the load phase, also after Ctrl+C; use it to release
// what setup created. A failure here is reported after the summary.
export function teardown(data: Data): void {
  console.log(`done testing as ${data.username}`);
}
