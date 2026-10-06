/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";

// The same test as basic-http.ts, written in plain JavaScript.
//
// Start the local target, then run:
//   go run ./benchmarks/server
//   loadtool run examples/basic-http.js --vus 10 --duration 10s
//
// Top-level code runs once per VU before the test starts.
// HTTP requests are only allowed inside the default function.

// Set BASE_URL to test another server: loadtool run -e BASE_URL=https://...
const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8080";

// Called repeatedly by every VU until the test duration ends.
export default function () {
  const res = http.get(`${BASE_URL}/api/test`, {
    headers: { Accept: "application/json" },
  });

  // A thrown error ends this iteration and is counted as a script error;
  // the test keeps running.
  if (res.status !== 200) {
    throw new Error(`unexpected status ${res.status} ${res.error}`);
  }
}
