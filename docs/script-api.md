# Script API

A LoadTool test is a TypeScript (`.ts`) or JavaScript (`.js`) file. The
API follows the shape of [k6](https://k6.io)'s. If you know k6, read
[Differences from k6](k6-differences.md) first.

- [A test script](#a-test-script)
- [`loadtool`](#loadtool): `check`, `sleep`, `group`
- [Globals](#globals): `__ENV`, `__VU`, `__ITER`, `console`
- [Imports](#imports): your own modules and JSON data
- [Lifecycle](#lifecycle): `setup` and `teardown`
- [Protocols](#protocols): HTTP, WebSocket, gRPC, GraphQL, Kafka

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

## Protocols

Each protocol has its own page:

| Module | Page |
|---|---|
| `loadtool/http` | [HTTP](protocols/http.md): requests, responses, cookies and sessions, HTTP/2 and connections |
| `loadtool/ws` | [WebSocket](protocols/websocket.md) |
| `loadtool/grpc` | [gRPC](protocols/grpc.md) |
| `loadtool/graphql` | [GraphQL](protocols/graphql.md) |
| `loadtool/kafka` | [Kafka](protocols/kafka.md) |

A test can use several of them in one iteration: see
[Mixed-protocol tests](examples/mixed-protocol.md). How the protocol
modules fit into the engine is in
[The protocol system](architecture/protocol-system.md).
