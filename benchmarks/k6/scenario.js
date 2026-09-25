// Benchmark scenario: keep in step with ../loadtool/scenario.ts and
// ../jmeter/scenario.jmx so all tools do identical work.
//
//   k6 run --vus 1000 --duration 60s benchmarks/k6/scenario.js
//   k6 run -e TARGET=http://10.0.0.5:8080/ --vus 1000 --duration 60s benchmarks/k6/scenario.js

import http from "k6/http";
import { check } from "k6";

const TARGET = __ENV.TARGET || "http://127.0.0.1:8080/";

export const options = {
  // Match LoadTool: bodies are drained but never kept for the script, and
  // redirects are not followed.
  discardResponseBodies: true,
  maxRedirects: 0,
};

export default function () {
  const res = http.get(TARGET);
  check(res, { "status is 200": (r) => r.status === 200 });
}
