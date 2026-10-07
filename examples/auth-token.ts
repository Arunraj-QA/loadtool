/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check } from "loadtool";

// Lifecycle: setup() runs once before the load, teardown() once after.
//
// Start the local target, then run:
//   go run ./benchmarks/server
//   loadtool run examples/lifecycle.ts --vus 10 --duration 10s
//
// Order: top-level code (once per VU) -> setup() -> load phase ->
// teardown(). Requests and checks in setup and teardown are not counted
// in the results.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8080";

interface Data {
  startedAt: string;
  expectedItems: number;
}

// Runs once. Throwing here stops the test before any load is generated,
// so a target that is down fails fast instead of producing a wall of
// errors. The return value is passed (as JSON) to every iteration and to
// teardown.
export function setup(): Data {
  const res = http.get(`${BASE_URL}/health`);
  if (res.status !== 200) {
    throw new Error(`target is not healthy: status ${res.status} ${res.error}`);
  }
  const sample = http.get(`${BASE_URL}/api/test`).json();
  return { startedAt: new Date().toISOString(), expectedItems: sample.items.length };
}

// Runs repeatedly in every VU, with this VU's own copy of the setup data.
export default function (data: Data): void {
  const res = http.get(`${BASE_URL}/api/test`);
  check(res, {
    "status is 200": (r) => r.status === 200,
    "item count unchanged": (r) => r.json().items.length === data.expectedItems,
  });
}

// Runs once after the load phase, also after Ctrl+C; use it to release
// what setup created. A failure here is reported after the summary.
export function teardown(data: Data): void {
  console.log(`test started at ${data.startedAt} is done`);
}
