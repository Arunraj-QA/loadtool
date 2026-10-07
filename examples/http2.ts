/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check } from "loadtool";

// HTTP/2: require it, and check each response used it.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/http2.ts --vus 10 --duration 10s
//
// httpVersion "2" insists on HTTP/2: over TLS for https:// URLs, or h2c
// (HTTP/2 without TLS, which the demo API speaks) for http:// URLs. A
// server without HTTP/2 then fails the request rather than being
// measured over HTTP/1.1.
//
// The default, "auto", uses HTTP/2 for https:// servers that offer it and
// HTTP/1.1 otherwise. "1.1" keeps HTTP/1.1 everywhere. The summary's
// "Protocols:" line shows what was used.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export const options = {
  httpVersion: "2",
  thresholds: { checks: ["rate==1"] },
};

export default function (): void {
  const res = http.get(`${BASE_URL}/api/products`);
  check(res, {
    "status is 200": (r) => r.status === 200,
    "served over HTTP/2": (r) => r.proto === "HTTP/2.0",
  });
}
