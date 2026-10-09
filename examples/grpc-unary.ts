/// <reference path="../types/loadtool.d.ts" />

import grpc from "loadtool/grpc";
import { check } from "loadtool";

// gRPC unary calls, described by a .proto file.
//
// Start the demo API (it serves gRPC on 127.0.0.1:8091), then run:
//   go run ./examples/server
//   loadtool run examples/grpc-unary.ts --vus 10 --duration 10s
//
// The client is created and the .proto file loaded in top-level code:
// every VU has its own client, and the file is parsed once for the run.

const GRPC_ADDR = __ENV.GRPC_ADDR || "127.0.0.1:8091";

const client = new grpc.Client();
client.load(["proto"], "greeter.proto"); // relative to this script
let connected = false; // each VU has its own

export const options = {
  thresholds: {
    grpc_req_failed: ["rate<0.01"],    // fewer than 1% of calls fail
    grpc_req_duration: ["p(95)<200"],  // 95% of calls within 200 ms
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  // Connect once per VU; the connection is kept across iterations and
  // closed at the end of the test. If connecting fails, the iteration ends
  // and the next one tries again.
  if (!connected) {
    const conn = client.connect(GRPC_ADDR, { plaintext: true });
    if (conn.error !== "") throw new Error(`connect: ${conn.error}`);
    connected = true;
  }

  const res = client.invoke(
    "greeter.Greeter/SayHello",
    { name: `VU ${__VU}` },
    { metadata: { "x-request-id": `${__VU}-${__ITER}` }, timeout: "2s" },
  );

  check(res, {
    "status is OK": (r) => r.status === 0,
    "greeted": (r) => r.message.message === `Hello, VU ${__VU}`,
    "request id echoed": (r) => r.headers["x-request-id"] === `${__VU}-${__ITER}`,
  });
}
