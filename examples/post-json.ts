/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check, sleep } from "loadtool";

// POST JSON: create orders and check the response body.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/post-json.ts --vus 5 --duration 10s
//
// Bodies are strings: JSON.stringify the payload and set Content-Type.
// Response bodies are discarded by default, to save memory; responseType:
// "text" keeps this request's body so the checks can read it.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export const options = {
  thresholds: {
    http_req_failed: ["rate<0.01"],
    checks: ["rate==1"],
  },
};

export default function (): void {
  // Vary the payload by VU and iteration.
  const productId = 1 + ((__VU + __ITER) % 5);
  const quantity = 1 + (__ITER % 3);

  const res = http.post(
    `${BASE_URL}/api/orders`,
    JSON.stringify({ productId, quantity }),
    {
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      responseType: "text", // keep the response body
    },
  );

  check(res, {
    "order: status is 201": (r) => r.status === 201,
    "order: echoes the product": (r) => r.json().productId === productId,
    "order: has a total": (r) => r.json().totalCents > 0,
  });

  sleep(0.2);
}
