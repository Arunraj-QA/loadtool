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
