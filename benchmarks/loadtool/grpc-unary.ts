/// <reference path="../../types/loadtool.d.ts" />

// Unary gRPC benchmark scenario (ADR-020): each VU has its own client,
// connected once, and calls SayHello in a loop against the demo API's
// greeter (go run ./examples/server -delay 10ms). Run by
// benchmarks/grpc-unary.ps1.

import grpc from "loadtool/grpc";

const ADDR = __ENV.GRPC_ADDR || "127.0.0.1:8091";
const client = new grpc.Client();
client.load(["../../examples/proto"], "greeter.proto");
let connected = false; // per VU: a failed connect is retried next iteration

export default function (): void {
  if (!connected) {
    const conn = client.connect(ADDR, { plaintext: true });
    if (conn.error !== "") throw new Error(`connect: ${conn.error}`);
    connected = true;
  }
  const res = client.invoke("greeter.Greeter/SayHello", { name: "bench" });
  if (res.status !== 0) {
    throw new Error(`status ${res.status_text}: ${res.error}`);
  }
}
