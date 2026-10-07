/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check, sleep } from "loadtool";

// Checks: assertions that are counted, not thrown.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/checks.ts --vus 10 --duration 10s
//
// The summary lists each check with its pass rate. A failed check does not
// stop the iteration or count as a request error.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export default function (): void {
  const list = http.get(`${BASE_URL}/api/products`);
  check(list, {
    "list: status is 200": (r) => r.status === 200,
    "list: is JSON": (r) => r.headers["Content-Type"] === "application/json",
    // json() throws on a non-JSON body; inside a check that just fails it.
    "list: has products": (r) => r.json().products.length > 0,
  });

  const one = http.get(`${BASE_URL}/api/products/3`);
  check(one, {
    "product: status is 200": (r) => r.status === 200,
    "product: right one": (r) => r.json().id === 3,
    "product: fast": (r) => r.timings.duration < 200, // milliseconds
  });

  sleep(0.5); // think time between iterations
}
