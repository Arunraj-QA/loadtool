/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check, sleep } from "loadtool";

// Scenarios: several workloads in one test, each with its own executor.
//
// Start the local target, then run:
//   go run ./benchmarks/server
//   loadtool run examples/scenarios.ts
//
// browsers ramp from 0 to 20 VUs and back, with think time; api calls
// arrive at a fixed rate, whatever the response time. Both run at once.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8080";

export const options = {
  scenarios: {
    browsers: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "5s", target: 20 }, // ramp up
        { duration: "10s", target: 20 }, // hold
        { duration: "5s", target: 0 }, // ramp down
      ],
      gracefulRampDown: "2s",
      exec: "browse",
    },
    api: {
      executor: "constant-arrival-rate",
      rate: 50, // iterations per timeUnit
      timeUnit: "1s",
      duration: "20s",
      preAllocatedVUs: 10, // enough VUs to keep up; otherwise starts are dropped
      exec: "callAPI",
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<200"],
    dropped_iterations: ["count==0"], // the api rate was really reached
  },
};

export function browse(): void {
  const res = http.get(`${BASE_URL}/api/test`);
  check(res, { "browse: status is 200": (r) => r.status === 200 });
  sleep(1); // think time: ramping VUs control concurrency, not rate
}

export function callAPI(): void {
  const res = http.get(`${BASE_URL}/health`);
  check(res, { "api: status is 200": (r) => r.status === 200 });
}
