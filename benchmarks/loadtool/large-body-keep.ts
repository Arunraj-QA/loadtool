/// <reference path="../../types/loadtool.d.ts" />

// The large-response scenario with every body kept, as before ADR-013:
// what a script pays when it opts in with discardResponseBodies: false.
// Run by benchmarks/body-memory.ps1.

import http from "loadtool/http";

const TARGET = (__ENV.BASE_URL || "http://127.0.0.1:8080") + "/api/large";

export const options = { discardResponseBodies: false };

export default function (): void {
  const res = http.get(TARGET);
  if (res.status !== 200) {
    throw new Error(`status ${res.status} ${res.error}`);
  }
}
