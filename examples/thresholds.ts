/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check } from "loadtool";

// Thresholds: pass/fail criteria for the whole test.
//
// Start the local target, then run:
//   go run ./benchmarks/server
//   loadtool run examples/thresholds.ts --vus 10 --duration 10s
//
// If any threshold fails, loadtool exits with code 99 (as k6 does), so a
// CI job fails. Each expression is "<aggregate> <op> <number>"; durations
// are in milliseconds.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8080";

export const options = {
  thresholds: {
    // 95% of requests faster than 200 ms, 99% faster than 500 ms.
    http_req_duration: ["p(95)<200", "p(99)<500"],
    // Fewer than 1% of requests fail.
    http_req_failed: ["rate<0.01"],
    // More than 99% of checks pass.
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  const res = http.get(`${BASE_URL}/api/test`);
  check(res, { "status is 200": (r) => r.status === 200 });
}
