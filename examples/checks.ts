/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check, sleep } from "loadtool";

// Checks: assertions that are counted, not thrown.
//
// Start the local target, then run:
//   go run ./benchmarks/server
//   loadtool run examples/checks.ts --vus 10 --duration 10s
//
// The summary lists each check with its pass rate. A failed check does not
// stop the iteration or count as a request error.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8080";

export default function (): void {
  const res = http.get(`${BASE_URL}/api/test`);

  check(res, {
    "status is 200": (r) => r.status === 200,
    "is JSON": (r) => r.headers["Content-Type"] === "application/json",
    // json() throws on a non-JSON body; inside a check that just fails it.
    "has three items": (r) => r.json().items.length === 3,
  });

  sleep(0.5);
}
