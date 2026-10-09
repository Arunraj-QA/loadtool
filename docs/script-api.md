# Script API

A LoadTool test is a TypeScript (`.ts`) or JavaScript (`.js`) file. The
API follows the shape of [k6](https://k6.io)'s. If you know k6, read
[Differences from k6](k6-differences.md) first.

- [A test script](#a-test-script)
- [`loadtool/http`](#loadtoolhttp): requests and responses
- [`loadtool`](#loadtool): `check`, `sleep`, `group`
- [Globals](#globals): `__ENV`, `__VU`, `__ITER`, `console`
- [Imports](#imports): your own modules and JSON data
- [Lifecycle](#lifecycle): `setup` and `teardown`
- [Cookies and sessions](#cookies-and-sessions)
- [HTTP versions and connections](#http-versions-and-connections)
- [`loadtool/ws`](#loadtoolws): WebSocket
- [`loadtool/grpc`](#loadtoolgrpc): gRPC
- [`loadtool/graphql`](#loadtoolgraphql): GraphQL
- [`loadtool/kafka`](#loadtoolkafka): Kafka producers and consumers

Options (`export const options`) are described in [Options](options.md).
Type declarations for editors are in
[`types/loadtool.d.ts`](../types/loadtool.d.ts): add
`/// <reference path="path/to/types/loadtool.d.ts" />` at the top of a
script.

## A test script

```typescript
import http from "loadtool/http";
import { check, sleep } from "loadtool";

export const options = { vus: 10, duration: "30s" };

export default function () {
  const res = http.get("http://127.0.0.1:8090/api/products");
  check(res, { "status is 200": (r) => r.status === 200 });
  sleep(1);
}
```

**Top-level code** runs once in each VU before the test starts. It is
the place for constants and data; HTTP requests and `sleep` are not
allowed there.

**The default function** is one *iteration*. Each VU calls it again and
again until its scenario ends.

**Errors in an iteration.**

- If an iteration throws, it ends, and the error is counted under "Script
  errs" in the summary (with the first message). The test goes on.
- JavaScript call depth is limited to 2,500 nested calls. Deeper recursion
  ends the iteration with
  `maximum call stack size of 2500 frames exceeded`, which `try/catch`
  cannot catch.

**`async` functions** (the default function, scenario functions,
`setup`, `teardown`) work, with errors reported as for other functions.
LoadTool has no event loop, though, and no LoadTool API returns a
Promise:

- **`await` on a value that is already available** works:
  `await Promise.resolve(1)`, or an `async` helper that does not wait on
  anything.
- **`await` on something that never resolves** ends the iteration with a
  script error that says so.
- **`async` gains nothing.** LoadTool calls are blocking, so a plain
  function does the same.

**TypeScript** types are removed with esbuild but **not type-checked**.
Error locations point at the original `.ts` lines.

## `loadtool/http`

```typescript
import http from "loadtool/http";            // the module object
import { get, post } from "loadtool/http";   // or single functions
```

| Function | Notes |
|---|---|
| `get(url, params?)` | |
| `post(url, body?, params?)` | `put`, `patch` and `del` take the same arguments |
| `request(method, url, body?, params?)` | Any method, e.g. `request("OPTIONS", url)` |

**`body`** is a string.

- To send JSON, use `JSON.stringify(...)` with a
  `Content-Type: application/json` header.
- An object body is a `TypeError`, not silently form-encoded.

```typescript
http.post(`${BASE}/api/orders`, JSON.stringify({ productId: 1, quantity: 2 }), {
  headers: { "Content-Type": "application/json" },
});
```

([`examples/post-json.ts`](../examples/post-json.ts) checks the response.)

**`params`** is `{ headers: { name: value }, cookies: { name: value },
responseType: "text" | "none" }`. Other keys (such as `timeout` or
`tags`) produce one warning per run and are ignored.

**Requests never throw on network errors.**

- A refused connection or a timeout (30 s per request) gives `status: 0`
  and an `error` message, and counts as a failed request.
- Redirects are not followed: a 3xx response is returned as it is.
- LoadTool sends only the headers you set; it does not add
  `Accept-Encoding: gzip`.

### Responses

| Field | Value |
|---|---|
| `status` | HTTP status, or `0` if no response was received |
| `proto` | `"HTTP/1.1"` or `"HTTP/2.0"`; `""` without a response |
| `error` | `""`, or the network error |
| `headers` | `{ "Content-Type": "..." }`, canonical names; repeated headers joined with `", "` |
| `body` | The body as a string; `null` if it was discarded (the default) or nothing arrived |
| `json()` | The body parsed as JSON; throws `SyntaxError` on invalid JSON, and `TypeError` if the body was discarded |
| `timings.duration` | Milliseconds from sending the request to reading the whole body |
| `url` | The request URL |
| `cookies` | Cookies this response set: `{ name: [{ name, value, domain, path, expires, max_age, http_only, secure }] }` |

**Response bodies are discarded by default.** Each body is read in full
(so timings include it), then thrown away. That keeps memory flat
whatever the response size: keeping 1 MB responses at 1,000 VUs needs
about 2 GB (ADR-013). To read bodies, ask for them:

| To keep | Set |
|---|---|
| Every body | `options.discardResponseBodies: false` |
| One request's body | `responseType: "text"` in that request's params |
| Not this one, when every body is kept | `responseType: "none"` |

```typescript
const res = http.get(`${BASE}/api/products/3`, { responseType: "text" });
check(res, { "right one": (r) => r.json().id === 3 });
```

**Reading a discarded body:**

- `res.body` is `null`, and LoadTool warns once per run, saying how to
  keep it.
- `res.json()` throws a `TypeError` with the same advice, so a check that
  uses it fails with that message.
- `status`, `headers`, `cookies` and timings are always available.
- Bodies in `setup` and `teardown` are always kept.

A request succeeds when it gets a 2xx or 3xx response.

## `loadtool`

```typescript
import { check, sleep, group } from "loadtool";
```

### `check(value, conditions)`

Runs each condition on `value` and counts a pass or fail per name. It
returns `true` if all passed.

```typescript
// The body is kept for this request so the check can read it.
const res = http.get(`${BASE}/api/products`, { responseType: "text" });
const ok = check(res, {
  "status is 200": (r) => r.status === 200,
  "has products": (r) => r.json().products.length > 0,
});
```

**Behaviour:**

- **A failed check** does not stop the iteration and is not a request
  error.
- **A condition that throws counts as failed.** For example, `r.json()`
  on an HTML error page fails "has products". The first message is shown in
  the summary.
- **A condition that is not a function** is a script error.
- **The summary** lists each check's pass rate. The `checks` threshold
  metric is the pass rate of all checks together.

### `sleep(seconds)`

Pauses the VU; fractions are allowed (`sleep(0.5)`).

- It returns early when the test ends, so it never delays the end.
- It is not allowed in top-level code.

### `group(name, fn)`

Runs `fn` and returns its result. Results are not broken down by group
yet.

## Globals

| Name | Value |
|---|---|
| `__ENV` | Environment variables, plus `--env KEY=VALUE` flags (which win). Changes a VU makes stay in that VU. |
| `__VU` | The VU number, from 1, unique across scenarios. It is 0 while options are read and in `setup`/`teardown`. |
| `__ITER` | The VU's iteration number, from 0 |
| `console` | `log`, `info`, `warn`, `error`, `debug`. Writes to stderr as `INFO  [VU 3] message`; objects are printed as JSON. |

Use `__ENV` for anything that differs between environments:

```typescript
const BASE = __ENV.BASE_URL || "http://127.0.0.1:8090";
```

```bash
loadtool run -e BASE_URL=https://staging.example.test test.ts
```

## Imports

**Allowed:**

- `loadtool` and `loadtool/http`.
- Files imported by relative path, which are bundled into the test:
  scripts (`./lib/api.ts`) and JSON data (`./data/users.json`).
  ([`examples/data-driven.ts`](../examples/data-driven.ts) uses both.)

**Not allowed:** npm packages and URLs. The error message lists what can
be imported.

**Scope.** Top-level variables belong to the script, as in an ES module.
They are not properties of `globalThis`.

## Lifecycle

A script can export `setup` and `teardown`
([`examples/auth-token.ts`](../examples/auth-token.ts)):

```typescript
export function setup() {
  const res = http.post(`${BASE}/api/login`,
    JSON.stringify({ username: "load-test", password: __ENV.API_PASSWORD }),
    { headers: { "Content-Type": "application/json" } });
  return { token: res.json().token };
}

export default function (data) {
  http.get(`${BASE}/api/me`, { headers: { Authorization: `Bearer ${data.token}` } });
}

export function teardown(data) {
  console.log("done");
}
```

**Order:**

1. **Top-level code** runs once in a lifecycle runtime (`__VU` is 0), where
   options are read.
2. **`setup()`** runs once in that runtime, before any VU starts.
3. **VUs start.** Each runs the top-level code and gets its own copy of
   setup's data.
4. **The load phase.** Every iteration calls the scenario's function with
   the data.
5. **`teardown(data)`** runs once in the lifecycle runtime, after every VU
   has stopped.
6. **The summary is printed.**

**Data:**

- Setup's return value is passed as JSON.
  - Functions and `undefined` are dropped.
  - A value JSON cannot represent (a cycle, a BigInt) fails setup.
  - Without `setup`, or when it returns nothing, `data` is `undefined`.
- Each VU parses its own copy once; its changes stay in that VU.
- Large setup data is held once per VU, so it costs memory × VUs.

**What setup and teardown can do:**

- Requests, `check` and `sleep` all work in `setup` and `teardown`, but
  none of it is counted in the results or thresholds.
- Response bodies are always kept there, even with
  `discardResponseBodies`.

**Failures and timeouts:**

- **Setup failure** ends the test: no load phase, no teardown, exit code 1.
  That covers a throw, `setupTimeout` (default `"60s"`) and Ctrl+C. Use it
  to stop early when the target is down.
- **Teardown runs whenever setup completed** (or there is no setup):
  - after a normal run;
  - after Ctrl+C in the load phase;
  - even if the VUs could not start.
- **Teardown failure** is shown in the summary
  (`Teardown: failed (...)`) and the exit code is 1. The results are
  printed in full first. Its limit is `teardownTimeout`, default `"60s"`.
- **A second Ctrl+C** exits at once and skips teardown.

## Cookies and sessions

**Each VU has its own cookie jar**
([`examples/sessions.ts`](../examples/sessions.ts)):

- Cookies that responses set are stored and sent on later requests to the
  same site, following their domain, path, expiry and `Secure` rules.
- Every iteration starts with an empty jar, like a new visitor.
  `options.noCookiesReset: true` keeps one session per VU for the whole
  test.
- VUs never see each other's cookies.
- `setup` and `teardown` share their own jar, so teardown can log out of
  setup's session.

```
VU 1 ─ session 1 ─ cookie jar A ─┐
VU 2 ─ session 2 ─ cookie jar B ─┼─ one shared connection pool
VU 3 ─ session 3 ─ cookie jar C ─┘
```

**Cookie rules** (RFC 6265, as implemented by Go's `net/http/cookiejar`):

| Cookie | Sent to |
|---|---|
| No `Domain` (host-only) | The host that set it only; not its subdomains or other hosts |
| `Domain=app.test` | `app.test` and its subdomains |
| `Path=/admin` | `/admin` and paths below it |
| `Secure` | `https://` requests only |

**Replacing and deleting:**

- A cookie with the same name, domain and path replaces the earlier one.
- `Max-Age=0`, a negative `Max-Age` or a past `Expires` deletes it: the
  usual logout.
- A cookie without `Max-Age` or `Expires` lasts until the jar is reset.
- No public-suffix list is used; it matters only for cookies set on
  domains like `co.uk`, which a test target should not do.

**Script access.** `params.cookies` adds cookies to one request, and
`res.cookies` shows what a response set. Reading and editing the jar
directly is not supported yet.

**Headers are per request.** Set them with `params.headers`; there are no
per-session default headers. For a header on every request, keep a params
object in a constant and pass it to each call.

## HTTP versions and connections

`options.httpVersion` selects the protocol
([`examples/http2.ts`](../examples/http2.ts)):

| Value | `http://` | `https://` |
|---|---|---|
| `"auto"` (default) | HTTP/1.1 | HTTP/2 if the server offers it, else HTTP/1.1 |
| `"1.1"` | HTTP/1.1 | HTTP/1.1 |
| `"2"` | HTTP/2 without TLS (h2c) | HTTP/2 only |

- **No fallback with `"2"`.** A server without HTTP/2 fails the request
  rather than being measured over HTTP/1.1.
- **Seeing the protocol.**
  - `res.proto` shows the protocol each response used.
  - The summary has a `Protocols:` line (for example `HTTP/2 9,000,
    HTTP/1.1 12`) whenever HTTP/2 was used, so you can tell whether
    `"auto"` negotiated it.
- **Extra connections.** HTTP/2 sends many VUs' requests over one
  connection. When every connection is at the server's stream limit, a
  new connection is dialled for each waiting request, so pushing a server
  far past its limit causes a burst of connections (as HTTP/1.1 does at
  start-up).
- **Low stream limits.** A server that allows fewer than 100 concurrent
  streams can refuse the first requests on a new connection; they count
  as failed requests.

**Connections:**

- They are kept alive and reused. All VUs share one pool with up to one
  connection per VU per host; HTTP/2 carries many requests per
  connection.
- `options.noConnectionReuse: true` opens a new connection for every
  request.
- Certificates are always verified.

## `loadtool/ws`

WebSocket sessions, in the same iteration as HTTP if you like
([`examples/websocket.ts`](../examples/websocket.ts)):

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

### The blocking style

Leave out the setup function, and `connect` returns a socket you drive
statement by statement
([`examples/websocket-request-reply.ts`](../examples/websocket-request-reply.ts)):

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

## `loadtool/grpc`

gRPC calls, described by `.proto` files or by server reflection
([`examples/grpc-unary.ts`](../examples/grpc-unary.ts),
[`examples/grpc-streaming.ts`](../examples/grpc-streaming.ts)):

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

## `loadtool/graphql`

GraphQL over LoadTool's HTTP transport: the same connections, HTTP/1.1
or HTTP/2 (`httpVersion`), and the VU's cookies as `loadtool/http`
([`examples/graphql-query.ts`](../examples/graphql-query.ts),
[`graphql-mutation.ts`](../examples/graphql-mutation.ts),
[`graphql-errors.ts`](../examples/graphql-errors.ts)):

```typescript
import graphql from "loadtool/graphql";
import { check } from "loadtool";

const URL = "http://127.0.0.1:8090/graphql";

export default function () {
  const res = graphql.query(URL, "query ($id: Int!) { product(id: $id) { name } }", {
    variables: { id: 3 },
    headers: { Authorization: "Bearer token" },
  });
  check(res, {
    "HTTP ok": (r) => r.http_ok,
    "GraphQL ok": (r) => r.ok,
    "name": (r) => r.data.product.name === "Ceramic cup",
  });
}
```

| Call | |
|---|---|
| `query(url, document, params?)` | Run a query |
| `mutation(url, document, params?)` | Run a mutation |
| `new graphql.Client(url, { headers })` | An endpoint with default headers; `client.query(document, params?)`, `client.mutation(...)` |
| `params` | `{ variables, headers, operationName, timeout }` |

Each operation is a JSON POST (`{ query, variables, operationName }`).

**HTTP success and GraphQL success are separate:**

| Field | Value |
|---|---|
| `http_ok` | The transport succeeded: an HTTP 2xx response |
| `ok` | The operation succeeded: `http_ok`, a JSON response, and no GraphQL `errors` |
| `data` | The response's `data`, or `null` |
| `errors` | The response's `errors`; `[]` when none |
| `error`, `error_code` | Why it is not `ok` (below) |
| `status`, `proto`, `headers`, `body`, `timings.duration` | As for HTTP responses |

**How each kind of failure shows up:**

| What happened | `http_ok` | `ok` | `error_code` |
|---|---|---|---|
| Success | true | true | `""` |
| **HTTP 200 with GraphQL `errors`** (partial `data` possible) | true | false | `server` |
| HTTP 4xx or 5xx | false | false | `server` |
| A 2xx body that is not GraphQL JSON | true | false | `protocol` |
| No response | false | false | `dial`, `dns`, `tls`, `timeout`, `closed` |
| Invalid URL | false | false | `invalid` |

**Metrics:** GraphQL operations are counted under `graphql_*` only, not
under `http_*`:

| Metric | Kind | Meaning |
|---|---|---|
| `graphql_req_duration` | trend | Operation latency |
| `graphql_reqs` | counter | Operations |
| `graphql_req_failed` | rate | Operations that were not `ok` |
| `graphql_errors` | counter | Operations whose response carried GraphQL errors |

**Not supported yet:**

- subscriptions;
- GET requests;
- persisted queries;
- batching;
- file uploads.

## `loadtool/kafka`

Kafka producers and consumers, on the franz-go client (ADR-022)
([`examples/kafka-producer.ts`](../examples/kafka-producer.ts),
[`kafka-consumer.ts`](../examples/kafka-consumer.ts),
[`kafka-throughput.ts`](../examples/kafka-throughput.ts),
[`kafka-errors.ts`](../examples/kafka-errors.ts)):

```ts
import kafka from "loadtool/kafka";
import { check } from "loadtool";

const producer = new kafka.Producer({ brokers: ["127.0.0.1:9092"], topic: "orders" });

export default function () {
  const res = producer.produce({ key: "order-1", value: JSON.stringify({ id: 1 }), headers: { source: "loadtool" } });
  check(res, { "acknowledged": (r) => r.ok });
}
```

Kafka is not request/response, so there are no `http`-like responses:
a producer sends messages and waits for the broker's acknowledgement, and
a consumer polls for messages.

| Call | Does |
|---|---|
| `new kafka.Producer({ brokers, topic?, tls?, timeout? })` | A producer; `timeout` bounds each delivery (default 30 s) |
| `producer.produce(message)` | Sends one message and waits for its acknowledgement; returns a result |
| `producer.produceBatch([message, ...])` | Sends the messages together; one result each, in order |
| `new kafka.Consumer({ brokers, topic, group?, startAt?, tls?, timeout? })` | A consumer of one topic; with `group`, it joins that consumer group |
| `consumer.consume({ max?, timeout? })` | Up to `max` messages (default 1); `[]` if none arrives within `timeout` (default 2 s) |
| `producer.close()`, `consumer.close()` | Close now (a consumer leaves its group); otherwise at the end of the test |
| `kafka.produce({ brokers, topic, ...message })` | A one-off produce, with a producer kept per configuration in the VU |
| `kafka.consume({ brokers, topic, group?, ..., max?, timeout? })` | A one-off consume, likewise |

A message is `{ value, key?, headers?, topic?, partition? }`. `value` is
required; `key`, `value` and header values are strings or `ArrayBuffer`s.
Messages with the same key go to the same partition; `partition` chooses
one explicitly. `topic` overrides the producer's.

**Clients are per VU.** A Producer or Consumer created in top-level code is
created in every VU, each with its own client, which connects on first use
and is reused across iterations. Calls that use the network are made in
iterations (or `setup`/`teardown`), not in top-level code. Consumers in the
same `group` share the topic's partitions, as an application's instances
would; without a group, each consumer reads every partition. `startAt`
(`"latest"`, the default, or `"earliest"`) is where a consumer starts
without a committed offset.

**Results never throw.** A produce result is
`{ ok, error, error_code, topic, partition, offset, timings: { duration } }`;
`partition` and `offset` are -1 when the message was not acknowledged.
A failed consume returns what arrived and sets `consumer.error` and
`consumer.error_code` (both `""` after a consume that worked); a consume
that times out with nothing to read is not an error.

| Failure | `error_code` |
|---|---|
| A broker error (unknown topic, not leader, message too large, ...) | `server` |
| Not acknowledged within the timeout | `timeout` (or `server` with the broker's last error) |
| Unreachable broker | `dial` or `timeout` |
| TLS handshake failure | `tls` |
| The producer or consumer was closed | `closed` |
| No value, no topic, a negative partition (never sent) | `invalid` |

A consumed message is
`{ topic, partition, offset, key, value, headers, timestamp, latency }`:
`key` is `null` without one, `timestamp` is when it was produced (Unix
milliseconds), and `latency` is the milliseconds from production to
consumption.

**Metrics:**

| Metric | Kind | Meaning |
|---|---|---|
| `kafka_produce_duration` | trend | From sending a message to its acknowledgement |
| `kafka_messages_produced` | counter | Acknowledged messages (its rate is the produce throughput) |
| `kafka_produce_failed` | rate | Messages that were not acknowledged (invalid ones included) |
| `kafka_consume_latency` | trend | End to end: from a message's production (its timestamp) to its consumption |
| `kafka_messages_consumed` | counter | Consumed messages |
| `kafka_consume_failed` | rate | Consumes that failed |

`kafka_consume_latency` compares the producer's clock with the consumer's,
so it is exact only when they are the same machine (as with LoadTool
producing and consuming) or their clocks are synchronized; messages
produced before the test began show how long they waited.

**Not supported:** Kafka Streams, transactions and exactly-once
production, administration (creating topics), manual offset commits
(groups commit automatically), and SASL authentication.
