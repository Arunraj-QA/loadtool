/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import ws from "loadtool/ws";
import { check } from "loadtool";

// HTTP and WebSocket in the same iteration: fetch a product over HTTP,
// then send its name over a WebSocket and wait for the reply. Each
// protocol is measured on its own (http_req_duration, ws_msg_latency),
// and the thresholds below cover both.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/mixed-http-websocket.ts --vus 10 --duration 10s
//
// All five protocols in one iteration: mixed-protocols.ts.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const WS_URL = BASE_URL.replace(/^http/, "ws") + "/ws/echo";

export const options = {
  thresholds: {
    http_req_failed: ["rate==0"],
    http_req_duration: ["p(95)<500"],
    ws_session_failed: ["rate==0"],
    ws_msg_latency: ["p(95)<500"],
    checks: ["rate==1"],
  },
};

export default function (): void {
  // HTTP: read a product.
  const res = http.get(`${BASE_URL}/api/products/${1 + (__ITER % 5)}`, { responseType: "text" });
  check(res, { "http: product found": (r) => r.status === 200 });
  if (res.status !== 200) return;
  const product = res.json() as { name: string };

  // WebSocket: send its name; each receive times the oldest unanswered
  // send (ws_msg_latency).
  const socket = ws.connect(WS_URL);
  socket.send(JSON.stringify({ watch: product.name }));
  const reply = socket.receive(5000);
  socket.close();
  check(reply, { "websocket: reply about the product": (m) => m !== null && JSON.parse(String(m)).watch === product.name });
}
