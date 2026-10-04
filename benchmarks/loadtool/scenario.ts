/// <reference path="../../types/loadtool.d.ts" />

import http from "loadtool/http";

// Benchmark scenario: keep in step with ../k6/scenario.js and
// ../jmeter/scenario.jmx so all tools do identical work.
//
//   loadtool run benchmarks/loadtool/scenario.ts --vus 1000 --duration 60s
//   loadtool run -e TARGET=http://10.0.0.5:8080/api/test --vus 1000 --duration 60s benchmarks/loadtool/scenario.ts

const TARGET = __ENV.TARGET || "http://127.0.0.1:8080/api/test";

// Bodies are not used, and the k6 scenario discards them too.
export const options = { discardResponseBodies: true };

export default function (): void {
  const res = http.get(TARGET);
  if (res.status !== 200) {
    throw new Error(`status ${res.status} ${res.error}`);
  }
}
