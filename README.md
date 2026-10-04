# LoadTool

LoadTool is an open-source API performance and load-testing engine written
in Go. Test scenarios are written in TypeScript and run by a Go engine that
uses one goroutine per virtual user (VU).

> **Status: early development (Phase 1 in progress).** `loadtool run`
> executes a TypeScript or JavaScript test script with goja, generates
> HTTP/1.1 load from one goroutine per VU, and prints a summary. The
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

| Flag | Default | Meaning |
|---|---|---|
| `-u, --vus` | `1` | Concurrent virtual users (one goroutine each) |
| `-d, --duration` | `10s` | How long VUs keep starting new iterations |
| `--graceful-stop` | `30s` | How long iterations still running when `--duration` ends may take to finish; `0` cancels them at once |
| `-e, --env` | | Set `KEY=VALUE` in the script's `__ENV` (repeatable; overrides the process environment) |

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
- `params` is `{ headers: { name: value } }`. Other keys produce a
  warning (once per run) and are ignored.
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
| `__VU` | The VU number, from 1. It is 0 while `options` are read. |
| `__ITER` | The VU's iteration number, from 0 |
| `console` | `log`, `info`, `warn`, `error`, `debug`. Writes to stderr as `INFO  [VU 3] message`; objects are printed as JSON. |

Imports:
- Scripts can import other files by relative path:
  `import { login } from "./helpers.ts"`. They are bundled into the test.
- Only `loadtool`, `loadtool/http` and relative paths can be imported.
  npm packages are not supported.
- Top-level variables belong to the script, as in an ES module. They are
  not properties of `globalThis`.

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
internal/httpclient/ HTTP/1.1 request execution
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
