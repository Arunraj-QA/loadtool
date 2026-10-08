/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import ws from "loadtool/ws";
import { check } from "loadtool";

// WebSocket, mixed with HTTP in one iteration: log in over HTTP, then open
// a WebSocket as the same user (the handshake sends the VU's cookies),
// exchange messages, and time each reply.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/websocket.ts --vus 10 --duration 10s
//
// ws.connect blocks until the socket closes. Handlers and timers run
// inside it, one session per call.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const WS_URL = BASE_URL.replace(/^http/, "ws") + "/ws/echo";
const MESSAGES = 5;

export const options = {
  thresholds: {
    ws_session_failed: ["rate<0.01"],  // fewer than 1% of sessions fail
    ws_msg_latency: ["p(95)<200"],     // 95% of replies within 200 ms
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  const login = http.post(
    `${BASE_URL}/api/login`,
    JSON.stringify({ username: `ws-${__VU}`, password: __ENV.API_PASSWORD || "demo" }),
    { headers: { "Content-Type": "application/json" } },
  );
  check(login, { "login: status is 200": (r) => r.status === 200 });

  let received = 0;
  const res = ws.connect(WS_URL, {}, (socket) => {
    socket.on("open", () => {
      for (let i = 0; i < MESSAGES; i++) {
        // reply: true times this send until the next message arrives
        // (ws_msg_latency); the echo server answers in order.
        socket.send(`message ${i}`, { reply: true });
      }
    });
    socket.on("message", (data) => {
      received++;
      check(data, { "ws: echoed": (d) => typeof d === "string" && d.startsWith("message") });
      if (received === MESSAGES) socket.close();
    });
    socket.on("error", (e) => console.warn(`ws error ${e.error_code}: ${e.error}`));
    // A guard: never keep a session open longer than 5 s.
    socket.setTimeout(() => socket.close(), 5000);
  });

  check(res, {
    "ws: connected": (r) => r.status === 101,
    "ws: no error": (r) => r.error === "",
    "ws: all replies": () => received === MESSAGES,
  });
}
