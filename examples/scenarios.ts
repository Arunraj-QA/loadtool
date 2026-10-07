/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check, sleep } from "loadtool";

// Scenarios: three workloads in one test, one per executor. They run at
// the same time.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/scenarios.ts
//
// - catalog:  constant-vus. 5 VUs browse for the whole test.
// - shoppers: ramping-vus. 0 -> 10 VUs and back; each places orders,
//   with think time.
// - api:      constant-arrival-rate. 50 product lookups per second,
//   whatever the response time.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export const options = {
  scenarios: {
    catalog: {
      executor: "constant-vus",
      vus: 5,
      duration: "20s",
      exec: "browse",
    },
    shoppers: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "5s", target: 10 }, // ramp up
        { duration: "10s", target: 10 }, // hold
        { duration: "5s", target: 0 }, // ramp down
      ],
      gracefulRampDown: "2s",
      exec: "shop",
    },
    api: {
      executor: "constant-arrival-rate",
      rate: 50, // iterations per timeUnit
      timeUnit: "1s",
      duration: "20s",
      preAllocatedVUs: 10, // enough VUs to keep up; otherwise starts are dropped
      exec: "lookup",
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<200"],
    http_req_failed: ["rate<0.01"],
    dropped_iterations: ["count==0"], // the api rate was really reached
  },
};

export function browse(): void {
  const res = http.get(`${BASE_URL}/api/products`);
  check(res, { "browse: status is 200": (r) => r.status === 200 });
  sleep(1); // think time: constant and ramping VUs control concurrency, not rate
}

export function shop(): void {
  const res = http.post(
    `${BASE_URL}/api/orders`,
    JSON.stringify({ productId: 1 + (__ITER % 5), quantity: 1 }),
    { headers: { "Content-Type": "application/json" } },
  );
  check(res, { "shop: order created": (r) => r.status === 201 });
  sleep(0.5);
}

export function lookup(): void {
  const res = http.get(`${BASE_URL}/api/products/${1 + (__ITER % 5)}`);
  check(res, { "api: status is 200": (r) => r.status === 200 });
}
