# ADR-020: gRPC module

- Status: Accepted (2026-10-09, Phase 2), with the connect-retry
  amendment
- Date: 2026-10-09
- Builds on: ADR-014 to ADR-018 (protocol modules), ADR-016 (errors),
  ADR-017 (blocking calls)

## Context

The roadmap's gRPC goal, as the user specified it on 2026-10-09:

- **Calls:** unary RPCs, server streaming, and client and bidirectional
  streaming "where practical".
- **Methods described by** server reflection or by `.proto` files.
- **Call options:** metadata, deadlines and cancellation.
- **Results:** latency and status recorded; checks, thresholds and the
  JSON and HTML reports preserved.
- **Ownership:** each VU owns its client and session state.

**The previous scope said unary only.** Phase 2 scope decision 2
(CLAUDE.md, 2026-10-08) limited gRPC to unary calls. The user's request
adds streaming, so that decision is updated: **unary and all three
streaming kinds, in the blocking style below.**

**The wire protocol must not be written by hand.** gRPC is HTTP/2
framing, length-prefixed protobuf messages, trailers and status codes;
an established library must handle it.

## Decision: libraries

| Need | Library | Licence | Why |
|---|---|---|---|
| gRPC client: HTTP/2 transport, framing, status, metadata, deadlines, streams | **`google.golang.org/grpc`** (grpc-go) | Apache-2.0 | The reference Go implementation, maintained by the gRPC project and the most used Go gRPC stack. It handles connection management, flow control, keepalive and cancellation through `context`. It also ships the server reflection service and its client stubs, so tests and reflection need nothing more. |
| Messages without generated code | **`google.golang.org/protobuf`**: `dynamicpb`, `protojson`, `protodesc`, `protoregistry` | BSD-3-Clause | The official protobuf runtime. `dynamicpb` builds messages from descriptors at run time. `protojson` converts them to and from JSON, the bridge to JavaScript objects. |
| `.proto` files without `protoc` | **`github.com/bufbuild/protocompile`** | Apache-2.0 | A pure-Go compiler from Buf, used by the Buf CLI. It turns `.proto` sources into descriptors in memory, so users need no `protoc`, and release builds stay static and cgo-free. |

**Alternatives considered:**

| Alternative | Why not |
|---|---|
| **A custom HTTP/2 + protobuf wire implementation** | Excluded by the request. It would also duplicate grpc-go's flow control, keepalive and status semantics, with more risk. |
| **`connectrpc.com/connect`** | It speaks the gRPC protocol too, but it is built around generated code. Dynamic, descriptor-driven calls are what a load tool needs, and grpc-go supports them directly through `ClientConn.Invoke` and `NewStream` with any `proto.Message`. |
| **`github.com/jhump/protoreflect`** | v1 is in maintenance in favour of protocompile, and v2 would add a dependency for reflection. A reflection client takes about 100 lines on grpc-go's own reflection stubs. |
| **Shelling out to `grpcurl` or `protoc`** | An external binary, a process per call, and no control over timing. |

**Rule:** all three libraries are imported only by
`internal/protocols/grpc`, its test service, and the demo API (ADR-018
§1, enforced by the import test).

## Decision: the module (`loadtool/grpc`)

**The shape follows k6's `k6/net/grpc` for unary calls,** so k6 scripts
port with few changes. Streams use the blocking style (ADR-017), not
k6's event-loop API.

```ts
import grpc from "loadtool/grpc";

const client = new grpc.Client();
client.load(["proto"], "greeter.proto");          // top-level code: parse once, cached for the run

let connected = false;                            // per VU

export default function () {
  if (!connected) connected = client.connect("127.0.0.1:8091", { plaintext: true }).error === "";  // or { reflect: true }
  const res = client.invoke("greeter.Greeter/SayHello", { name: "Ada" }, {
    metadata: { "x-request-id": "1" }, timeout: "2s",
  });
  // res: { status, status_text, message, headers, trailers, error, error_code, timings: { duration } }

  const stream = client.stream("greeter.Greeter/Chat", { timeout: "5s" });     // any streaming kind
  stream.send({ name: "a" });
  stream.closeSend();
  for (let m = stream.recv(); m !== null; m = stream.recv()) { /* ... */ }
  // stream.status, stream.error_code once ended
}
```

### Client

| Call | Behaviour |
|---|---|
| `new grpc.Client()` | A client in the VU that creates it; allowed in top-level code |
| `client.load(importPaths, ...files)` | Parses `.proto` files (relative to the script's directory), cached in the run, so 1,000 VUs parse once. Allowed in top-level code. |
| `client.connect(address, { plaintext, reflect, timeout })` | Dials and waits until the connection is ready (default timeout 30 s). A refused attempt is retried with grpc-go's backoff until the timeout (see the amendment). With `reflect: true`, it fetches the server's descriptors by reflection; these are cached in the run, per address. Returns `{ error, error_code }`, and does not throw on network failure. |
| `client.invoke(method, request, { metadata, timeout })` | One unary call (default timeout 30 s), returning the result above. `message` is converted from protobuf when first read. |
| `client.stream(method, { metadata, timeout })` | Opens a stream for a streaming method of any kind, and returns a stream object |
| `client.close()` | Closes the connection |

### Streams (blocking, no goroutines of their own)

| Call | Behaviour |
|---|---|
| `send(message)` | Sends one message; returns `false` if the stream is over |
| `closeSend()` | Ends the client side |
| `recv()` | Returns the next message, or `null` once the stream ended. `status`, `status_text`, `error`, `error_code` and `trailers` then hold the final status. |
| `close()` | Cancels the stream if it is still open |

**How each kind works:**

- **Server streaming:** `send` one request, `closeSend`, then `recv`
  until `null`.
- **Client streaming:** `send` many, `closeSend`, then one `recv`.
- **Bidirectional:** interleave them.

**The timeout bounds the whole stream.** grpc-go's `RecvMsg` takes no
per-call timeout. Every call runs on the VU's goroutine and is bounded by
the stream's context: the VU's context plus the timeout. No goroutine is
started.

### Ownership and lifecycle (ADR-018 §6)

**The run owns shared, read-only data:**

- parsed `.proto` descriptors, keyed by import paths and files;
- reflected descriptors, keyed by address;
- the TLS settings.

**A VU owns its clients.** Each `new grpc.Client()` belongs to the VU
that runs it. Its `ClientConn` is registered with `VU.OnClose` and kept
across iterations (connect once, as in k6) until `client.close()` or the
end of the run.

**This is scope decision 3:** one connection per VU per client. It is
checked by the 1,000-VU measurement.

**Streams belong to the iteration that opened them.** One still open
when the iteration ends is cancelled, recorded as a normal end, and
warned about once. When the test ends, everything is cancelled and
nothing is recorded.

**Clients made in setup** belong to the setup/teardown runtime. Setup
data is JSON, so connections never cross VUs.

### Metrics (ADR-015)

| Family | Kind | Meaning |
|---|---|---|
| `grpc_req_duration` | Trend | Unary call latency, from send to the response and trailers |
| `grpc_reqs` | Counter | Unary calls |
| `grpc_req_failed` | Rate | Unary calls whose status was not OK |
| `grpc_streams` | Counter | Streams opened |
| `grpc_stream_duration` | Trend | From open to the final status |
| `grpc_stream_failed` | Rate | Streams that ended with a status other than OK |
| `grpc_stream_msgs_sent`, `grpc_stream_msgs_received` | Counter | Stream messages |

**The status code is in each result** (`status`, `status_text`), so
checks can assert it. A per-code count would need dynamic metric names,
which ADR-015 does not have.

### Errors (ADR-016)

| gRPC status | `error_code` |
|---|---|
| `OK` | `""` |
| `DeadlineExceeded` | `timeout` |
| `Unavailable` caused by a transport error | Classified (`dial`, `dns`, `tls`), else `closed` |
| `Canceled` not caused by the end of the test | `closed` |
| Every other status (`NotFound`, `Internal`, …) | `server` |

**Other failures:**

- **An unknown method, or a request that does not fit its message type**
  (bad JSON fields), is `invalid` and is never sent.
- **A connect failure** returns `{ error, error_code }`, and the client
  stays unconnected. A later call is `invalid` ("not connected").

**Misuse throws a `TypeError`:**

- a network call in top-level code (only `new grpc.Client()` and `load`
  are allowed there);
- a missing method name;
- `stream` on a unary method, or `invoke` on a streaming one.

### Test services

**A dynamic test service** (`internal/protocols/grpc/grpctest`) is built
from the `.proto` fixture `examples/proto/greeter.proto`. It uses
`dynamicpb` messages, so no `protoc` and no generated code are needed:

- unary `SayHello`;
- server-streaming `LotsOfReplies`;
- client-streaming `LotsOfGreetings`;
- bidirectional `Chat`;
- `Fail`, which returns any status the request names, for error tests;
- `fail_code` on a streaming request, which makes the stream end with
  that status;
- the server's `-delay`, before each `SayHello` reply and each
  `LotsOfReplies` reply, for timeout tests.

**Coverage.** Every call kind (unary, server, client, bidirectional) is
tested with `.proto` files and with reflection, and with an error status,
a deadline and cancellation. Most of this is in
`internal/protocols/grpc/matrix_test.go`.

**Reflection is enabled** through grpc-go's `reflection.NewServerV1`,
with the fixture's descriptors as its resolver.

**The demo API serves it** on a second port (default `127.0.0.1:8091`),
so the examples run locally.

## Consequences

- **Larger binary.** gRPC and protobuf add to its size; this is measured
  in the benchmark record.
- **Several different `.proto` sets can be loaded per run.** Each is
  parsed once.
- **The protocol wiring changes in two ways** (ADR-018 amendment):
  - **Capitalized exports are constructors** (`new grpc.Client()`).
  - **Module runs start before the script's top-level code,** so
    top-level `client.load` works. `RunEnv` gains the script's
    directory, and loses `MaxVUs`, which nothing used and which is not
    known that early.
- **Not in Phase 2:**
  - compression;
  - per-message receive timeouts;
  - load balancing across several addresses;
  - `grpc.Stream` with event callbacks, as in k6.

## Amendment (2026-10-09): connect retries, found by the benchmark

**What happened.** The first benchmark session failed about 95 % of
calls at 500 and 1,000 VUs (`benchmarks/results/2026-10-09-grpc-unary/`,
session 1). Two things combined:

- **`connect` gave up on the first failed attempt.** When hundreds of
  VUs connect at once, some attempts are refused while the server's
  listen backlog is full; grpc-go would have retried them a moment later.
- **The script connected only when `__ITER === 0`.** After a failed
  connect, every later iteration called an unconnected client and failed
  in microseconds. The high calls-per-second figures were these failures.

**The decision:**

- **`connect` waits until the connection is ready or the timeout
  passes,** treating a failed attempt as retryable, as blocking dials in
  grpc-go do. If the timeout passes after a failed attempt, the error is
  `dial`, otherwise `timeout`.
- **Scripts connect until connected:** a `connected` flag per VU. The
  examples, the docs and the benchmark script use this pattern.
- **`TestConnectRetriesUntilTimeout`** covers a server that starts after
  the first attempt.

