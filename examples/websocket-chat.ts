/// <reference path="../types/loadtool.d.ts" />

import ws from "loadtool/ws";
import { check } from "loadtool";

// A WebSocket chat: VUs share rooms, and every message goes to everyone
// in the room, the sender included. Each iteration joins the VU's room,
// posts a message, reads the room's traffic until its own message comes
// back, and leaves.
//
// Start the demo API (examples/server, which serves /ws/chat), then run:
//   go run ./examples/server
//   loadtool run examples/websocket-chat.ts --vus 20 --duration 10s
//
// ROOMS (default 4) sets how many rooms the VUs are spread over: fewer
// rooms mean more traffic per VU.
//
// ws_msg_latency times each send until the next message received, which
// in a busy room may be someone else's. The check on the VU's own message
// is what shows delivery; ws_msgs_received shows the room's traffic.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const ROOMS = Number(__ENV.ROOMS || 4);

export const options = {
  thresholds: {
    ws_session_failed: ["rate==0"],
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  const me = `vu${__VU}`;
  const room = `room-${__VU % ROOMS}`;
  const socket = ws.connect(`${BASE_URL.replace(/^http/, "ws")}/ws/chat?room=${room}`);
  check(socket, { "joined the room": (s) => s.status === 101 && s.error === "" });

  const sent = Date.now();
  socket.send(JSON.stringify({ from: me, text: `message ${__ITER}`, sent }));

  // Others' messages may arrive before ours; read until ours is back, or
  // give up after 2 s without any message.
  let delivered = false;
  for (let m = socket.receive(2000); m !== null; m = socket.receive(2000)) {
    const msg = JSON.parse(String(m));
    if (msg.from === me && msg.sent === sent) {
      delivered = true;
      break;
    }
  }
  socket.close();

  check(delivered, { "own message delivered to the room": (d) => d });
}
