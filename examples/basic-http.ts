/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";

// The smallest useful test: GET an API, with a constant number of VUs.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/basic-http.ts --vus 10 --duration 10s
//
// --vus and --duration give one constant-VU scenario: 10 VUs, each
// calling the default function in a loop for 10 seconds.
//
// Top-level code runs once per VU before the test starts.
// HTTP requests are only allowed inside the default function.

// Set BASE_URL to test another server: loadtool run -e BASE_URL=https://...
const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

// Called repeatedly by every VU until the test duration ends.
export default function (): void {
  const res = http.get(`${BASE_URL}/api/products`, {
    headers: { Accept: "application/json" },
  });

  // A thrown error ends this iteration and is counted as a script error;
  // the test keeps running.
  if (res.status !== 200) {
    throw new Error(`unexpected status ${res.status} ${res.error}`);
  }
}
