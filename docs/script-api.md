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
  const res = http.get("https://api.example.test/products");
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
http.post(`${BASE}/orders`, JSON.stringify({ sku: "A1", qty: 2 }), {
  headers: { "Content-Type": "application/json" },
});
```

**`params`** is `{ headers: { name: value }, cookies: { name: value } }`.
Other keys (such as `timeout` or `tags`) produce one warning per run and
are ignored.

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
| `body` | The body as a string; `null` if bodies are discarded or nothing arrived |
| `json()` | The body parsed as JSON; throws `SyntaxError` on invalid JSON |
| `timings.duration` | Milliseconds from sending the request to reading the whole body |
| `url` | The request URL |
| `cookies` | Cookies this response set: `{ name: [{ name, value, domain, path, expires, max_age, http_only, secure }] }` |

**Bodies are kept by default.** If a test never reads them, set
`options.discardResponseBodies: true`: it saves an allocation per
request, and `body` is then `null`.

A request succeeds when it gets a 2xx or 3xx response.

## `loadtool`

```typescript
import { check, sleep, group } from "loadtool";
```

### `check(value, conditions)`

Runs each condition on `value` and counts a pass or fail per name. It
returns `true` if all passed.

```typescript
const ok = check(res, {
  "status is 200": (r) => r.status === 200,
  "has items": (r) => r.json().items.length > 0,
});
```

**Behaviour:**

- **A failed check** does not stop the iteration and is not a request
  error.
- **A condition that throws counts as failed.** For example, `r.json()`
  on an HTML error page fails "has items". The first message is shown in
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
const BASE = __ENV.BASE_URL || "http://127.0.0.1:8080";
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
([`examples/lifecycle.ts`](../examples/lifecycle.ts)):

```typescript
export function setup() {
  const res = http.post(`${BASE}/login`, JSON.stringify({ user: "load" }),
    { headers: { "Content-Type": "application/json" } });
  return { token: res.json().token };
}

export default function (data) {
  http.get(`${BASE}/orders`, { headers: { Authorization: `Bearer ${data.token}` } });
}

export function teardown(data) {
  http.post(`${BASE}/logout`, data.token);
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

**Script access.** `params.cookies` adds cookies to one request, and
`res.cookies` shows what a response set. Reading and editing the jar
directly is not supported yet.

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
- **`res.proto`** shows the protocol each response used.

**Connections:**

- They are kept alive and reused. All VUs share one pool with up to one
  connection per VU per host; HTTP/2 carries many requests per
  connection.
- `options.noConnectionReuse: true` opens a new connection for every
  request.
- Certificates are always verified.
