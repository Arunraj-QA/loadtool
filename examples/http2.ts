/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import { check } from "loadtool";

// HTTP/2. With the default httpVersion "auto", HTTPS servers that offer
// HTTP/2 are tested over HTTP/2 and everything else over HTTP/1.1.
//
//   loadtool run -e BASE_URL=https://staging.example.test examples/http2.ts --vus 20 --duration 30s
//
// httpVersion "2" insists on HTTP/2: over TLS, or h2c (HTTP/2 without TLS)
// for http:// URLs. A server without HTTP/2 then fails the request rather
// than being measured over HTTP/1.1. "1.1" keeps HTTP/1.1 everywhere.

const BASE_URL = __ENV.BASE_URL || "https://127.0.0.1:8443";

export const options = {
  httpVersion: "2",
  thresholds: { checks: ["rate==1"] },
};

export default function (): void {
  const res = http.get(`${BASE_URL}/`);
  check(res, {
    "status is 200": (r) => r.status === 200,
    "served over HTTP/2": (r) => r.proto === "HTTP/2.0",
  });
}
