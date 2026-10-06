# LoadTool

LoadTool is an open-source API performance and load-testing engine written
in Go. Test scenarios are written in TypeScript and run by a Go engine that
uses one goroutine per virtual user (VU).

> **Status: early development (Phase 1 in progress).** `loadtool run`
> executes a TypeScript or JavaScript test script with goja, generates
> HTTP/1.1 and HTTP/2 load from one goroutine per VU, and prints a summary. The
> k6-shaped script API is being built out; see
> [docs/architecture.md](docs/architecture.md) for the plan.

## Phase 0 goal

Phase 0 builds the smallest useful engine:

- `loadtool run <script.ts>` executes a TypeScript test file through
  [goja](https://github.com/dop251/goja)
- HTTP/1.1 load generation with one goroutine per VU
- Basic metrics: request, success and error counts, error rate, and
  p50 / p90 / p95 / p99 latency
- A console summary
- Unit tests and Go benchmarks

**Exit criterion:** `loadtool run` executes a scripted HTTP test at 1,000 VUs
from a laptop, using less memory than JMeter at the same VU count. Measured
on 2026-10-01: at 1,000 VUs LoadTool's peak private bytes (190–210 MB) and
working set were below JMeter's (1,366–1,371 MB) on every run. Caveats: one
laptop with the server on the same machine, and JMeter with its default JVM
settings. Details are in
[`benchmarks/results/2026-10-01-phase0-all-tools/`](benchmarks/results/2026-10-01-phase0-all-tools/).

Distributed execution, dashboards, storage, browser testing and AI analysis
are planned for later phases and are out of scope for Phase 0.

## Build and run

Requires Go (see `go.mod` for the minimum version).

```bash
go build -o bin/loadtool ./cmd/loadtool
./bin/loadtool run examples/basic-http.ts --vus 10 --duration 10s
```

Released versions are published as static binaries for Linux, macOS and
Windows (amd64 and arm64), with `checksums.txt`, on the
[GitHub releases page](https://github.com/Arunraj-QA/loadtool/releases).
No release has been published yet; [docs/releasing.md](docs/releasing.md)
describes how one is made.

### In CI

`loadtool run` exits 0 when every threshold passes, 99 when a threshold
fails and 1 on any other failure, so a CI job fails on a failed load
test. For GitHub Actions there is a ready-made action (`action.yml`);
other systems need three commands. See [docs/ci](docs/ci/README.md).

| Flag | Default | Meaning |
|---|---|---|
| `-u, --vus` | `1` | Concurrent virtual users (one goroutine each) |
| `-d, --duration` | `10s` | How long VUs keep starting new iterations |
| `--graceful-stop` | `30s` | How long iterations still running when `--duration` ends may take to finish; `0` cancels them at once |
| `-e, --env` | | Set `KEY=VALUE` in the script's `__ENV` (repeatable; overrides the process environment) |
| `--summary-json` | | Also write the end-of-test summary as versioned JSON to this file ([format](docs/json-summary.md)) |
| `--report-html` | | Also write a self-contained HTML report with charts over time to this file |

Press Ctrl+C to stop early. A partial summary is printed and the exit code is 1.

### Options in the script

A script can declare its own settings:

```typescript
export const options = { vus: 10, duration: "30s" };
```

Each setting is taken from the first source that sets it
([ADR-006](docs/decisions/ADR-006-options-and-precedence.md)):

1. a CLI flag you typed (`--vus`, `--duration`)
2. an environment variable (`LOADTOOL_VUS`, `LOADTOOL_DURATION`)
3. the script's `options`
4. the default (1 VU, 10 s)

Notes:
- `duration` accepts `"30s"` / `"1m30s"`, or a number of milliseconds.
- Options LoadTool does not support yet produce a warning and are
  ignored, so scripts written for k6 still run.
- `vus`/`duration` describe one constant-VU scenario. For anything else
  use [scenarios](#scenarios). A typed `--vus`/`--duration` (or
  `LOADTOOL_VUS`/`LOADTOOL_DURATION`) replaces the script's scenarios or
  stages with one constant-VU scenario, with a warning.

## Writing a test

A test is a `.ts` or `.js` file that exports a default function. Every VU
calls it repeatedly until the duration ends. See
[`examples/basic-http.ts`](examples/basic-http.ts), or
[`examples/basic-http.js`](examples/basic-http.js) for the same test in plain
JavaScript.

```typescript
import http from "loadtool/http";

export default function () {
  const res = http.get("http://localhost:8080/");
  if (res.status !== 200) {
    throw new Error(`unexpected status ${res.status}`);
  }
}
```

The script API follows the shape of k6's
([ADR-005](docs/decisions/ADR-005-k6-shaped-script-api.md)). It is being
built out in Phase 1 ([ADR-008](docs/decisions/ADR-008-test-dsl.md)). The
`loadtool/http` module provides:

| Call | Returns |
|---|---|
| `http.get(url, params?)` | response |
| `http.post(url, body?, params?)`; also `put`, `patch`, `del` | response |
| `http.request(method, url, body?, params?)` | response |

Every function can also be imported by name:
`import { get, post } from "loadtool/http"`. Phase 0 scripts that used a
global `http` need the import line added; the error message says so.

- `body` is a string. Send JSON with `JSON.stringify(...)` and a
  `Content-Type` header; an object body is an error, not form data.
- `params` is `{ headers: { name: value }, cookies: { name: value } }`.
  Other keys produce a warning (once per run) and are ignored.
- Type declarations for editors are in
  [`types/loadtool.d.ts`](types/loadtool.d.ts).

A response has:

| Field | Value |
|---|---|
| `status` | HTTP status, or `0` if no response was received |
| `error` | `""`, or the transport error |
| `headers` | `{ "Content-Type": "..." }`; repeated headers are joined with `", "` |
| `body` | The body as a string; `null` if bodies are discarded or nothing arrived |
| `json()` | The body parsed as JSON; throws `SyntaxError` on invalid JSON |
| `timings.duration` | Milliseconds, including reading the body |
| `url` | The request URL |
| `proto` | `"HTTP/1.1"` or `"HTTP/2.0"` |
| `cookies` | Cookies this response set: `{ name: [{ name, value, domain, path, expires, max_age, http_only, secure }] }` |

Bodies are read and kept by default. Set
`export const options = { discardResponseBodies: true }` when a test does
not use them; this saves an allocation per request.

### More of the script API

The `loadtool` module ([ADR-007](docs/decisions/ADR-007-script-modules-and-globals.md)):

```typescript
import http from "loadtool/http";
import { check, sleep, group } from "loadtool";

export default function () {
  group("home page", () => {
    const res = http.get("http://localhost:8080/");
    check(res, { "status is 200": (r) => r.status === 200 });
  });
  sleep(1); // seconds; fractions allowed
}
```

- `check(value, { name: condition })` runs each condition on `value` and
  counts a pass or fail per name; it returns `true` if all passed. A
  condition that throws (for example `r.json()` on an HTML error page)
  counts as failed. Failed checks do not stop the iteration or count as
  request errors; the summary lists each check's pass rate. See
  [`examples/checks.ts`](examples/checks.ts).
- `sleep(seconds)` pauses the VU. It ends early when the test ends, and is
  not allowed in top-level code.
- `group(name, fn)` runs `fn` and returns its result. Results are not
  broken down by group yet.

Globals:

| Name | Value |
|---|---|
| `__ENV` | Environment variables, plus `--env KEY=VALUE` flags (which win). Changes a VU makes stay in that VU. |
| `__VU` | The VU number, from 1. It is 0 while `options` are read and in `setup`/`teardown`. |
| `__ITER` | The VU's iteration number, from 0 |
| `console` | `log`, `info`, `warn`, `error`, `debug`. Writes to stderr as `INFO  [VU 3] message`; objects are printed as JSON. |

Imports:
- Scripts can import other files by relative path:
  `import { login } from "./helpers.ts"`. They are bundled into the test.
- Only `loadtool`, `loadtool/http` and relative paths can be imported.
  npm packages are not supported.
- Top-level variables belong to the script, as in an ES module. They are
  not properties of `globalThis`.

### Setup and teardown

A script can export `setup` and `teardown` functions
([ADR-008](docs/decisions/ADR-008-test-dsl.md); see
[`examples/lifecycle.ts`](examples/lifecycle.ts)):

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

A test runs in this order:

1. **Top-level code** runs once in a lifecycle runtime (`__VU` 0), where
   `options` are read.
2. **`setup()`** runs once in that runtime, before any VU exists.
3. **VUs start**: each runs the top-level code and gets its own copy of
   the setup data.
4. **Load phase**: every iteration calls `default(data)`.
5. **`teardown(data)`** runs once in the lifecycle runtime, after all VUs
   have stopped.
6. The summary is printed.

Rules:
- **Data.** `setup`'s return value is passed as JSON. Functions and
  `undefined` are dropped, and a value JSON cannot represent (a cycle, a
  BigInt) fails setup.
  - Each VU parses its own copy once. Changes a VU makes stay in that VU,
    across its iterations.
  - `teardown` gets the data as setup returned it.
  - Without `setup`, or when it returns nothing, `data` is `undefined`.
  - Large setup data is held once per VU, so it costs memory × VUs.
- **Not counted.** `setup` and `teardown` may make requests, `sleep` and run
  `check`. None of it is counted in the results or thresholds.
  - Response bodies are always kept there, even with
    `discardResponseBodies`.
- **Setup failure** ends the test: no load phase, no teardown, exit code 1.
  This includes a throw, `setupTimeout`, or Ctrl+C during setup. Use it to
  fail fast when the target is down.
- **Teardown runs whenever setup completed**, or the script has no setup:
  after a normal run, after Ctrl+C during the load phase, and even if the
  VUs failed to start.
- **Teardown failure** is shown in the summary (`Teardown: failed (...)`)
  and the exit code is 1, but the results are printed in full first. This
  covers a throw, `teardownTimeout`, or Ctrl+C during teardown.
- **Timeouts.** `options.setupTimeout` and `options.teardownTimeout` bound
  each function (default `"60s"`).
- **Ctrl+C:**
  - during the load phase, it stops the load, and teardown still runs.
  - a second Ctrl+C exits at once and skips teardown.

### Sessions and connections

Each VU has its own cookie jar
([ADR-009](docs/decisions/ADR-009-sessions-and-connection-reuse.md); see
[`examples/sessions.ts`](examples/sessions.ts)).

```typescript
export default function () {
  http.post(`${BASE}/login`, JSON.stringify({ user: "load", password: "secret" }),
    { headers: { "Content-Type": "application/json" } }); // sets a session cookie
  http.get(`${BASE}/account`); // sent with that cookie
}
```

Cookies:
- **Kept automatically.** Cookies that responses set are stored and sent on
  later requests to the same site, following their domain, path, expiry
  and `Secure` rules.
- **Reset each iteration.** Every iteration starts with an empty jar, so it
  behaves like a new visitor. Set `options.noCookiesReset: true` to keep
  one session per VU for the whole test.
- **Not shared.** VUs never see each other's cookies.
- **setup and teardown** share one jar, so teardown can log out of setup's
  session. Neither is visible to the VUs.
- **Extra cookies.** `params.cookies` adds cookies to one request; reading
  and editing the jar from the script (k6's `http.cookieJar()`) is not
  supported yet.

Connections:
- **Reused by default.** Connections are kept alive and reused, and all VUs
  share one pool with up to one connection per VU per host.
- **`options.noConnectionReuse: true`** opens a new connection for every
  request, to measure connection set-up or to test balancers that track
  connections.
- **Not supported:** k6's `noVUConnectionReuse`.

HTTP versions ([ADR-010](docs/decisions/ADR-010-http2.md); see
[`examples/http2.ts`](examples/http2.ts)), set with `options.httpVersion`:

| Value | `http://` | `https://` |
|---|---|---|
| `"auto"` (default) | HTTP/1.1 | HTTP/2 if the server offers it, else HTTP/1.1 |
| `"1.1"` | HTTP/1.1 | HTTP/1.1 (Phase 0 behaviour; use it to compare with JMeter) |
| `"2"` | HTTP/2 without TLS (h2c) | HTTP/2 only |

Notes:
- **No fallback with `"2"`.** A server without HTTP/2 fails the request
  instead of silently using HTTP/1.1.
- **One connection, many requests.** HTTP/2 carries many VUs' requests
  over few connections.
- **Checking the protocol.** `res.proto` shows which protocol each response
  used.

### Scenarios

Scenarios describe the workload: how many VUs run, and when they start
iterations ([ADR-008](docs/decisions/ADR-008-test-dsl.md); see
[`examples/scenarios.ts`](examples/scenarios.ts)). Several scenarios run
at the same time.

```typescript
export const options = {
  scenarios: {
    browsers: {
      executor: "ramping-vus",
      stages: [{ duration: "1m", target: 50 }, { duration: "5m", target: 50 }, { duration: "1m", target: 0 }],
      exec: "browse", // the exported function to run; default: default
    },
    orders: {
      executor: "constant-arrival-rate",
      rate: 20, timeUnit: "1s", duration: "7m",
      preAllocatedVUs: 40,
      startTime: "30s",
      exec: "placeOrder",
    },
  },
};
```

| Executor | Fields | What it does |
|---|---|---|
| `constant-vus` | `vus` (1), `duration` | `vus` VUs run iterations back to back |
| `ramping-vus` | `startVUs` (1), `stages`, `gracefulRampDown` (`30s`) | The number of active VUs moves linearly through the stages |
| `constant-arrival-rate` | `rate`, `timeUnit` (`1s`), `duration`, `preAllocatedVUs` | `rate` iterations start per `timeUnit`, however long they take |

Every scenario also accepts `exec`, `startTime` (`0s`) and `gracefulStop`
(`30s`). Defaults are in brackets.

How they behave:
- **VUs are created up front.** All VUs of all scenarios are created, and
  run the top-level code, before the clock starts, so memory is known up
  front. `__VU` numbers are unique across scenarios, in scenario name order.
- **`stages`.** `options.stages` on its own is the shorthand for one
  `ramping-vus` scenario.
  - VU *n* (from 1) is active while the stage line is at least *n*.
  - A pure ramp from 0 to 10 therefore reaches the 10th VU only at its
    very end; add a hold stage to run all of them.
  - A stage with duration `0s` jumps at once.
- **Ramping down.** A VU removed by a ramp-down finishes its current
  iteration, for up to `gracefulRampDown`; after that the iteration is
  cancelled.
- **Arrival rate.** Starts follow a fixed schedule, so the rate does not
  drift.
  - If no VU is free, a start is **dropped**, not queued. The summary shows
    "Dropped: N iterations", and the `dropped_iterations` metric counts them.
  - Raise `preAllocatedVUs` if that happens. `maxVUs` (growing the pool
    during the test) is not supported yet.
- **Validation.**
  - Errors name the scenario and field, and stop the run before setup. This
    covers a field that does not belong to the executor (such as `rate` on
    `constant-vus`) and an `exec` that is not exported.
  - Unknown scenario keys produce a warning.
  - Options that set both `scenarios` and `vus`/`duration`/`stages` are
    an error.
- **`--graceful-stop`** applies to the `vus`/`duration` shorthand only;
  scenarios use their own `gracefulStop`.

### Thresholds

Thresholds are pass/fail criteria for the whole test
([ADR-008](docs/decisions/ADR-008-test-dsl.md); see
[`examples/thresholds.ts`](examples/thresholds.ts)). If any fails,
`loadtool run` prints the full summary and then exits with code **99**, as
k6 does, so a CI job fails.

```typescript
export const options = {
  thresholds: {
    http_req_duration: ["p(95)<200", "p(99)<500"], // milliseconds
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
  },
};
```

An expression is `<aggregate> <op> <number>`, with `<`, `<=`, `>`, `>=`,
`==` or `!=`.

| Metric | Aggregates | Meaning |
|---|---|---|
| `http_req_duration` | `avg`, `min`, `max`, `med`, `p(N)` | Latency of every request sent, in ms |
| `http_req_failed` | `rate` | Failed requests ÷ requests (0–1) |
| `http_reqs` | `count`, `rate` | Requests; `rate` is per second |
| `checks` | `rate` | Passed checks ÷ checks run (0–1) |
| `iterations` | `count`, `rate` | Iterations that ran to their end |
| `dropped_iterations` | `count`, `rate` | Always 0 until arrival-rate scenarios exist |

Rules:
- **Checked before the test starts.** A typo, an unknown metric or an
  unsupported aggregate stops the run before setup, with exit code 1.
- **Evaluated once, at the end**, against the numbers the summary prints.
- **A threshold with no data fails.** For example, `checks` in a script
  that never calls `check`. It is shown as "no data".
- **Percentiles are approximate.** They are within ±0.78 %, and a result
  that close to its limit is marked `≈`.
- **Exit code 1 takes precedence.** If the run was interrupted or
  teardown failed, the exit code is 1, not 99.
- **Not supported yet:** the object form `{ threshold, abortOnFail }`,
  thresholds on tagged sub-metrics such as `http_req_duration{status:200}`,
  and custom metrics. They are errors, not ignored.

How results are counted:
- A request succeeds when it gets a 2xx or 3xx response. Redirects are not
  followed.
- A transport failure (connection refused, timeout) does not throw. The
  response has `status: 0` and an `error` message, and the request counts as
  failed.
- If the script throws, that iteration ends and is counted under
  **Script errs**. The test keeps running, and the first error message is
  shown in the summary.
- Top-level code runs once per VU before the test starts. HTTP requests are
  not allowed there.
- JavaScript call depth is limited to 2,500 nested calls per VU. Deeper
  recursion ends the iteration with a script error
  (`maximum call stack size of 2500 frames exceeded`); `try/catch` cannot
  catch it.
- When `--duration` ends, no new iterations start, but running ones may
  finish for up to `--graceful-stop`, and their requests are counted.
  Requests still running after that, or when you press Ctrl+C, are
  cancelled and not counted.
- The summary shows latency twice:
  - **all requests sent**, failed ones included. This is the same
    population k6's `http_req_duration` and JMeter use.
  - **successful requests only.** Fast failures such as refused
    connections pull the first set down; the second is unaffected.
- Percentiles come from a fixed-size histogram and are within ±0.78 % of
  the exact value. Min, max, mean and counts are exact. Memory does not
  grow with test length
  ([ADR-004](docs/decisions/ADR-004-latency-histogram.md)).
- A request that could not be sent at all (for example an invalid URL)
  counts as a failed request but adds no latency sample.
- LoadTool sends only the headers the script sets. It does not add
  `Accept-Encoding: gzip` on its own.

TypeScript types are stripped (with esbuild) but **not type-checked**.
Error locations refer to the original `.ts` lines.

## Development

```bash
go test ./...
go vet ./...
go test -race ./...
go test -bench=. -benchmem ./...
```

The `-race` check needs cgo and a C compiler. CI
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs it on Linux
for every push, and runs vet and tests on Linux and Windows.

## Repository layout

```text
cmd/loadtool/        CLI entrypoint
internal/cli/        Cobra commands: flags, printing, exit codes
internal/runner/     Orchestrates a run: script, options, VUs, engine, result
internal/report/     Result model and outputs (console summary)
internal/config/     Run settings and validation
internal/engine/     Goroutine-per-VU scheduler (protocol-agnostic)
internal/script/     TypeScript/JavaScript loading and per-VU goja runtimes
internal/httpclient/ HTTP request execution (HTTP/1.1, HTTP/2)
internal/metrics/    Per-VU recording and percentile aggregation
examples/            Example test scripts
benchmarks/          Recorded benchmark results
docs/decisions/      Architecture decision records (ADR-NNN)
```

See [docs/architecture.md](docs/architecture.md) for how the pieces fit
together, [docs/workflows.md](docs/workflows.md) for workflow diagrams, and
the [architecture decision records](docs/decisions/) for why.

## License

[Apache License 2.0](LICENSE)
