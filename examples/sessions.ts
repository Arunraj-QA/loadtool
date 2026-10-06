/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check } from "loadtool";

// Sessions: each VU keeps its own cookies, like a browser.
//
// This example needs an application with a login that sets a session
// cookie. Point it at yours:
//   loadtool run -e BASE_URL=https://staging.example.test examples/sessions.ts --vus 10 --duration 30s
//
// Every iteration starts with an empty cookie jar, so each one logs in as
// a new visitor. Set noCookiesReset: true to log in once per VU instead.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:3000";

export const options = {
  // noCookiesReset: true,
  thresholds: { checks: ["rate>0.99"] },
};

export default function (): void {
  const login = http.post(
    `${BASE_URL}/login`,
    JSON.stringify({ user: __ENV.USER_NAME || "load-test", password: __ENV.PASSWORD || "" }),
    { headers: { "Content-Type": "application/json" } },
  );
  check(login, {
    "logged in": (r) => r.status === 200,
    // res.cookies lists the cookies this response set.
    "session cookie set": (r) => Object.keys(r.cookies).length > 0,
  });

  // The session cookie is sent automatically.
  const account = http.get(`${BASE_URL}/account`);
  check(account, { "account visible": (r) => r.status === 200 });
}
