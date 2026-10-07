/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check, sleep } from "loadtool";

// Cookie sessions: each VU keeps its own cookies, like a browser.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/sessions.ts --vus 10 --duration 10s
//
// The login response sets a session cookie; LoadTool stores it in the
// VU's cookie jar and sends it on later requests, with no code needed.
// Every iteration starts with an empty jar, so each one is a new visitor
// who logs in. Set noCookiesReset: true to log in once per VU instead.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export const options = {
  // noCookiesReset: true,
  thresholds: { checks: ["rate>0.99"] },
};

const JSON_HEADERS = { headers: { "Content-Type": "application/json" } };

export default function (): void {
  // A different user per VU, so sessions are easy to tell apart.
  const username = `visitor-${__VU}`;
  const login = http.post(
    `${BASE_URL}/api/login`,
    JSON.stringify({ username, password: __ENV.API_PASSWORD || "demo" }),
    JSON_HEADERS,
  );
  check(login, {
    "login: status is 200": (r) => r.status === 200,
    // res.cookies lists the cookies this response set.
    "login: session cookie set": (r) => r.cookies.session !== undefined,
  });

  // The session cookie is sent automatically.
  const me = http.get(`${BASE_URL}/api/me`);
  check(me, { "me: logged in as this VU's user": (r) => r.json().username === username });

  sleep(0.2);

  // Logging out deletes the cookie (Max-Age < 0). A request now would be
  // anonymous and get 401, which counts as a failed request (any status
  // of 400 or more does).
  const logout = http.post(`${BASE_URL}/api/logout`);
  check(logout, { "logout: status is 204": (r) => r.status === 204 });
}
