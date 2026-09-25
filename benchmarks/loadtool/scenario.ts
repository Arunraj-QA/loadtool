/// <reference path="../../examples/loadtool.d.ts" />

// Benchmark scenario: keep in step with ../k6/scenario.js and
// ../jmeter/scenario.jmx so all tools do identical work.
//
//   loadtool run benchmarks/loadtool/scenario.ts --vus 1000 --duration 60s
//
// LoadTool has no environment variables in scripts yet, so change
// TARGET here when the server runs on another machine.

const TARGET = "http://127.0.0.1:8080/";

export default function (): void {
  const res = http.get(TARGET);
  if (res.status !== 200) {
    throw new Error(`status ${res.status} ${res.error}`);
  }
}
