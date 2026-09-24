/// <reference path="./loadtool.d.ts" />

// Run with:
//   loadtool run examples/basic-http.ts --vus 10 --duration 10s
//
// Top-level code runs once per VU before the test starts.
// HTTP requests are only allowed inside the default function.

const BASE_URL = "http://localhost:8080";

// Called repeatedly by every VU until the test duration ends.
export default function (): void {
  const res = http.get(`${BASE_URL}/`, {
    headers: { Accept: "application/json" },
  });

  // A thrown error ends this iteration and is counted as a script error;
  // the test keeps running.
  if (res.status !== 200) {
    throw new Error(`unexpected status ${res.status} ${res.error}`);
  }
}
