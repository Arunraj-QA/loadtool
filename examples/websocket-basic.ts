/// <reference path="../types/loadtool.d.ts" />

import ws from "loadtool/ws";
import { check } from "loadtool";

// The smallest WebSocket test: connect, send one message, wait for the
// echo, close. ws.connect blocks until the socket closes; the callback
// sets up handlers for what happens meanwhile.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/websocket-basic.ts --vus 10 --duration 10s
//
// For more messages per session, see websocket-request-reply.ts (blocking
// style) and websocket.ts (callbacks, after an HTTP login).

const WS_URL = (__ENV.BASE_URL || "http://127.0.0.1:8090").replace(/^http/, "ws") + "/ws/echo";

export const options = {
  thresholds: {
    ws_session_failed: ["rate==0"],
    checks: ["rate==1"],
  },
};

export default function (): void {
  const message = `hello from VU ${__VU}`;
  let reply = "";
  const res = ws.connect(WS_URL, {}, (socket) => {
    socket.on("open", () => socket.send(message));
    socket.on("message", (data) => {
      reply = String(data);
      socket.close();
    });
    // A guard: never wait more than 5 s for the echo.
    socket.setTimeout(() => socket.close(), 5000);
  });
  check(res, { "connected": (r) => r.status === 101 && r.error === "" });
  check(reply, { "echoed": (r) => r === message });
}
