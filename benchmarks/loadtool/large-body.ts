/// <reference path="../../types/loadtool.d.ts" />

// Large-response memory scenario (ADR-013): GET /api/large and read
// nothing. With the default (bodies discarded), memory must not grow with
// the body size. Run by benchmarks/body-memory.ps1.

import http from "loadtool/http";

const TARGET = (__ENV.BASE_URL || "http://127.0.0.1:8080") + "/api/large";

export default function (): void {
  const res = http.get(TARGET);
  if (res.status !== 200) {
    throw new Error(`status ${res.status} ${res.error}`);
  }
}
