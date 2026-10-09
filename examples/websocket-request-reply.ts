/// <reference path="../types/loadtool.d.ts" />

import ws from "loadtool/websocket"; // the same module as "loadtool/ws"
import { check } from "loadtool";

// WebSocket in the blocking style: connect, send, receive, close, one
// statement after another. Each receive times the oldest unanswered send
// (ws_msg_latency).
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/websocket-request-reply.ts --vus 10 --duration 10s
//
// For servers that push messages on their own, or many messages at once,
// the callback style (examples/websocket.ts) fits better.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const WS_URL = BASE_URL.replace(/^http/, "ws") + "/ws/echo";

export const options = {
  thresholds: {
    ws_session_failed: ["rate<0.01"],
    ws_msg_latency: ["p(95)<200"],
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  const socket = ws.connect(WS_URL);
  check(socket, { "connected": (s) => s.status === 101 && s.error === "" });

  for (let i = 0; i < 3; i++) {
    socket.send(`hello ${i}`);
    // Wait up to 5 s for the reply; null if none arrives.
    const response = socket.receive(5000);
    check(response, {
      "message received": (r) => r != null,
      "echoed": (r) => r === `hello ${i}`,
    });
  }

  socket.close();
}
