/// <reference path="../types/loadtool.d.ts" />

import grpc from "loadtool/grpc";
import { check } from "loadtool";

// gRPC streaming, described by server reflection (no .proto file).
//
// Start the demo API (it serves gRPC on 127.0.0.1:8091), then run:
//   go run ./examples/server
//   loadtool run examples/grpc-streaming.ts --vus 5 --duration 10s
//
// Streams are blocking: send, closeSend, then recv until it returns null.
// Each call blocks this VU until it is done; the stream's timeout bounds
// the whole stream.

const GRPC_ADDR = __ENV.GRPC_ADDR || "127.0.0.1:8091";
const client = new grpc.Client();

export const options = {
  thresholds: {
    grpc_stream_failed: ["rate<0.01"],
    grpc_stream_duration: ["p(95)<500"],
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  if (__ITER === 0) {
    // reflect: true asks the server to describe its services.
    const conn = client.connect(GRPC_ADDR, { plaintext: true, reflect: true });
    if (conn.error !== "") throw new Error(`connect: ${conn.error}`);
  }

  // Server streaming: one request, many replies.
  const replies = client.stream("greeter.Greeter/LotsOfReplies", { timeout: "5s" });
  replies.send({ name: "Ada", count: 5 });
  replies.closeSend();
  let received = 0;
  while (replies.recv() !== null) received++;
  check(replies, {
    "server stream: 5 replies": () => received === 5,
    "server stream: OK": (s) => s.status === 0,
  });

  // Client streaming: many requests, one reply.
  const greetings = client.stream("greeter.Greeter/LotsOfGreetings");
  for (const name of ["a", "b", "c"]) greetings.send({ name });
  greetings.closeSend();
  const summary = greetings.recv();
  check(summary, { "client stream: all names": (m) => m !== null && m.message === "Hello, a, b, c" });
  greetings.recv(); // read to the end, so the status is final

  // Bidirectional: one reply per request, in turn.
  const chat = client.stream("greeter.Greeter/Chat");
  for (const name of ["x", "y"]) {
    chat.send({ name });
    const reply = chat.recv();
    check(reply, { "chat: answered": (m) => m !== null && m.message === `Hello, ${name}` });
  }
  chat.closeSend();
  chat.recv(); // null: the server ended the stream
}
