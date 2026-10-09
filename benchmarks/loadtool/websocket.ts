// WebSocket benchmark (benchmarks/protocol.ps1): each VU holds one
// connection to the demo API's echo for the whole run, sending a 64-byte
// message every second and timing its echo (ws_msg_latency). So the
// number of open connections is the number of VUs. Run with a short
// --graceful-stop: sessions end when the test does.
import ws from "loadtool/ws";

const URL = (__ENV.BASE_URL || "http://127.0.0.1:8090").replace(/^http/, "ws") + "/ws/echo";
const PAYLOAD = "x".repeat(64);

export default function () {
  ws.connect(URL, {}, (socket) => {
    socket.on("open", () => {
      socket.setInterval(() => socket.send(PAYLOAD, { reply: true }), 1000);
    });
  });
}
