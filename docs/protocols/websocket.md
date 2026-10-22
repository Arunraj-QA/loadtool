# WebSocket (`loadtool/ws`)

[Script API](../script-api.md) · Protocols: [HTTP](http.md) · [WebSocket](websocket.md) · [gRPC](grpc.md) · [GraphQL](graphql.md) · [Kafka](kafka.md)

WebSocket sessions, in the same iteration as HTTP if you like
([`examples/websocket.ts`](../../examples/websocket.ts)):

```typescript
import ws from "loadtool/ws";
import { check } from "loadtool";

export default function () {
  const res = ws.connect("ws://127.0.0.1:8090/ws/echo", {}, (socket) => {
    socket.on("open", () => socket.send("hello", { reply: true }));
    socket.on("message", (data) => {
      check(data, { "echoed": (d) => d === "hello" });
      socket.close();
    });
    socket.setTimeout(() => socket.close(), 5000); // a guard
  });
  check(res, { "connected": (r) => r.status === 101 && r.error === "" });
}
```

There are two styles: **callbacks**, below, and a **blocking** style
([further down](#the-blocking-style)) for request/reply tests.

**`ws.connect(url, params?, setup)` blocks until the socket closes.**

1. It connects (`ws://` or `wss://`).
2. It calls `setup(socket)`, where you register handlers and timers.
3. It fires `open`, then runs the handlers as messages arrive.
4. It returns once the socket is closed.

Each call is one session. The handshake sends `params.headers` and the
VU's cookies, so a login over HTTP carries over.

| Socket | |
|---|---|
| `on("open", fn)` | The connection is ready |
| `on("message", fn(data))` | A message arrived: a string, or an `ArrayBuffer` for binary |
| `on("close", fn(code))` | The session ended, with its close code (1006 if the connection dropped) |
| `on("error", fn(e))` | A send or receive failed: `e.error`, `e.error_code` |
| `send(text, { reply })`, `sendBinary(buffer, { reply })` | Send a message; returns `false` if it could not be sent |
| `close(code = 1000)` | Close the session |
| `setTimeout(fn, ms)`, `setInterval(fn, ms)` | Timers inside the session |

**Result:**

| Field | Value |
|---|---|
| `status` | 101 when connected; the HTTP status of a refused handshake; 0 without a response |
| `error`, `error_code` | `""`, or why the session failed |
| `timings` | `connecting` and `duration`, in ms |

**Latency per message:**

- **`send(data, { reply: true })` times that message** until the next
  message received, recorded as `ws_msg_latency`.
- **Replies are matched in order,** so this fits echo and
  request/reply servers.
- **Sends without `reply` are not timed,** so servers that push
  messages on their own do not distort it.

**Failures:**

| When | What happens |
|---|---|
| **The handshake fails** (refused, DNS, TLS, an HTTP error status) | The callback is not called; `res.error` and `res.error_code` say why |
| **A send or receive fails during a session** | `error` fires and the session ends |
| **The peer closes with a code other than 1000 or 1001** | The session counts as failed (`error_code` `server`) |
| **The test ends** | Open sessions close at once and are not counted |

**Metrics.** These appear in the summary under "Protocol metrics", in
the JSON summary and in the HTML report, and thresholds can use them:

| Metric | Kind | Meaning |
|---|---|---|
| `ws_connecting` | trend | Handshake time |
| `ws_sessions` | counter | Connection attempts |
| `ws_session_failed` | rate | Sessions that failed (handshake or abnormal end) |
| `ws_session_duration` | trend | Session length |
| `ws_msgs_sent`, `ws_msgs_received` | counter | Messages |
| `ws_msg_latency` | trend | Reply latency of sends marked `reply` |
| `ws_errors` | counter | Send and receive errors |

**Limits:**

- **Messages:** up to 1 MiB.
- **Handshake:** up to 30 s.
- **Not supported yet:** subprotocols, compression and ping/pong events.
- **Top-level code:** `ws.connect` is not allowed there, as with HTTP
  requests.

## The blocking style

Leave out the setup function, and `connect` returns a socket you drive
statement by statement
([`examples/websocket-request-reply.ts`](../../examples/websocket-request-reply.ts)):

```typescript
import ws from "loadtool/websocket"; // or "loadtool/ws": the same module
import { check } from "loadtool";

export default function () {
  const socket = ws.connect("ws://127.0.0.1:8090/ws/echo");
  socket.send("hello");
  const response = socket.receive(5000); // the next message, or null
  check(response, { "message received": (r) => r != null });
  socket.close();
}
```

| Socket | |
|---|---|
| `send(text, { reply })`, `sendBinary(buffer, { reply })` | Send; `false` if it could not be sent. Each send is timed until a later `receive` returns a message, unless `reply: false` |
| `receive(timeoutMs = 30000)` | The next message (string or `ArrayBuffer`), or `null` on a timeout, a close or the end of the test |
| `close(code = 1000)` | Close and wait until closed |
| `status`, `error`, `error_code`, `closed`, `url`, `timings` | The session so far |

**How it behaves:**

- **A failed handshake** gives a closed socket with `error` and
  `error_code`; `send` returns `false` and `receive` returns `null`.
- **A receive timeout** sets `error_code` to `"timeout"` and counts in
  `ws_errors`, but the socket stays open.
- **A socket left open** is closed when the iteration ends, with a
  warning.
- **Replies are taken in order:** `receive` returns the next message,
  whatever it is. For servers that push messages on their own, use the
  callback style.

## Examples

Runnable against the demo API ([how](../../examples/README.md)):

| Example | Shows |
|---|---|
| [`websocket-basic.ts`](../../examples/websocket-basic.ts) | The smallest test: connect, send, check the echo |
| [`websocket.ts`](../../examples/websocket.ts) | Callbacks, after an HTTP login; reply latency |
| [`websocket-request-reply.ts`](../../examples/websocket-request-reply.ts) | The blocking style |
| [`websocket-chat.ts`](../../examples/websocket-chat.ts) | A chat with rooms (the demo API's `/ws/chat`) |
| [`mixed-http-websocket.ts`](../../examples/mixed-http-websocket.ts) | HTTP and WebSocket in one iteration |
