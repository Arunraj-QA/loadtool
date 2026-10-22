# gRPC (`loadtool/grpc`)

[Script API](../script-api.md) · Protocols: [HTTP](http.md) · [WebSocket](websocket.md) · [gRPC](grpc.md) · [GraphQL](graphql.md) · [Kafka](kafka.md)

gRPC calls, described by `.proto` files or by server reflection
([`examples/grpc-unary.ts`](../../examples/grpc-unary.ts),
[`examples/grpc-streaming.ts`](../../examples/grpc-streaming.ts)):

```typescript
import grpc from "loadtool/grpc";
import { check } from "loadtool";

const client = new grpc.Client();
client.load(["proto"], "greeter.proto"); // top-level code: parsed once per run
let connected = false; // per VU

export default function () {
  if (!connected) {
    const conn = client.connect("127.0.0.1:8091", { plaintext: true });
    if (conn.error !== "") throw new Error(conn.error); // retried next iteration
    connected = true;
  }
  const res = client.invoke("greeter.Greeter/SayHello", { name: "Ada" }, {
    metadata: { "x-request-id": "1" }, timeout: "2s",
  });
  check(res, { "OK": (r) => r.status === 0 && r.message.message === "Hello, Ada" });
}
```

**Client:**

| Call | |
|---|---|
| `new grpc.Client()` | A client for this VU; allowed in top-level code |
| `load(importPaths, ...files)` | Parse `.proto` files (paths relative to the script); allowed in top-level code. A parse error throws. |
| `connect(address, { plaintext, reflect, timeout })` | Connect and wait until ready. `plaintext: true` for gRPC without TLS; `reflect: true` describes the methods by server reflection. Returns `{ error, error_code }`. Refused attempts are retried until the timeout (default 30 s). The connection is kept across iterations: connect once per VU, and again if it failed (see the example). |
| `invoke(method, request, { metadata, timeout })` | One unary call to `"package.Service/Method"` |
| `stream(method, { metadata, timeout })` | Open a stream (any streaming kind) |
| `close()` | Close the connection; the end of the test does this too |

**`invoke` result:**

| Field | Value |
|---|---|
| `status`, `status_text` | The gRPC status: `0` / `"OK"`, `5` / `"NotFound"`, … |
| `message` | The reply as an object (`null` on failure); default values are included |
| `headers`, `trailers` | Response metadata |
| `error`, `error_code` | `""`, or the status message and a category: `timeout` (deadline), `dial`, `closed`, `server` (any other status), `invalid` (never sent: unknown method, bad request, not connected) |
| `timings.duration` | Milliseconds |

**Streams are blocking.** Each call blocks the VU until it is done, and
the `timeout` bounds the whole stream (default 30 s):

```typescript
const s = client.stream("greeter.Greeter/LotsOfReplies");
s.send({ name: "Ada", count: 5 });
s.closeSend();
for (let m = s.recv(); m !== null; m = s.recv()) { /* ... */ }
// s.status, s.error_code: the final status
```

| Kind | Pattern |
|---|---|
| Server streaming | `send` one request, `closeSend`, `recv` until `null` |
| Client streaming | `send` many, `closeSend`, `recv` the reply (then `null`) |
| Bidirectional | `send` and `recv` in turn, then `closeSend` |

**How streams behave:**

- **`close()` ends a stream early,** which counts as a normal end.
- **A stream left open** is closed when the iteration ends, with a
  warning.

**Metrics:**

| Metric | Kind | Meaning |
|---|---|---|
| `grpc_req_duration` | trend | Unary call latency |
| `grpc_reqs` | counter | Unary calls |
| `grpc_req_failed` | rate | Unary calls whose status was not OK |
| `grpc_streams` | counter | Streams opened |
| `grpc_stream_duration` | trend | From open to the final status |
| `grpc_stream_failed` | rate | Streams that ended with a status other than OK |
| `grpc_stream_msgs_sent`, `grpc_stream_msgs_received` | counter | Stream messages |

**Not supported yet:**

- compression;
- per-message receive timeouts;
- several addresses (load balancing);
- `insecureSkipVerify` (certificates are always verified).

## Examples

Runnable against the demo API ([how](../../examples/README.md)):

| Example | Shows |
|---|---|
| [`grpc-unary.ts`](../../examples/grpc-unary.ts) | Unary calls from a `.proto` file |
| [`grpc-streaming.ts`](../../examples/grpc-streaming.ts) | Server, client and bidirectional streams, by reflection |
