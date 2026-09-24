# LoadTool

LoadTool is an open-source API performance and load-testing engine written
in Go. Test scenarios are written in TypeScript and run by a Go engine that
uses one goroutine per virtual user (VU).

> **Status: early development (Phase 0).** `loadtool run` executes a
> TypeScript or JavaScript test script with goja, generates HTTP/1.1 load
> from one goroutine per VU, and prints a summary.

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
from a laptop, using less memory than JMeter at the same VU count. This has
**not** been measured yet. Results will be recorded in
[`benchmarks/`](benchmarks/).

Distributed execution, dashboards, storage, browser testing and AI analysis
are planned for later phases and are out of scope for Phase 0.

## Build and run

Requires Go (see `go.mod` for the minimum version).

```bash
go build -o bin/loadtool ./cmd/loadtool
./bin/loadtool run examples/basic.ts --vus 100 --duration 30s
```

| Flag | Default | Meaning |
|---|---|---|
| `-u, --vus` | `1` | Concurrent virtual users (one goroutine each) |
| `-d, --duration` | `10s` | How long VUs keep iterating |

Press Ctrl+C to stop early. A partial summary is printed and the exit code is 1.

## Writing a test

A test is a `.ts` or `.js` file that exports a default function. Every VU
calls it repeatedly until the duration ends. See
[`examples/basic.ts`](examples/basic.ts), or
[`examples/basic.js`](examples/basic.js) for the same test in plain
JavaScript.

```typescript
export default function () {
  const res = http.get("http://localhost:8080/");
  if (res.status !== 200) {
    throw new Error(`unexpected status ${res.status}`);
  }
}
```

The Phase 0 script API is intentionally small. It is a single `http` global:

| Call | Returns |
|---|---|
| `http.get(url, params?)` | response |
| `http.request(method, url, body?, params?)` | response |

- `params` is `{ headers: { name: value } }`.
- A response is `{ status, error, timings: { duration } }`, with the
  duration in milliseconds.
- Response bodies are not exposed to scripts yet.
- Type declarations for editors are in
  [`examples/loadtool.d.ts`](examples/loadtool.d.ts).

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
- A script is a single file: `import` statements are not supported yet.
- Requests cut off by the end of the test are not counted.

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
internal/cli/        Cobra commands and console summary
internal/config/     Run settings and validation
internal/engine/     Goroutine-per-VU scheduler (protocol-agnostic)
internal/script/     TypeScript/JavaScript loading and per-VU goja runtimes
internal/httpclient/ HTTP/1.1 request execution
internal/metrics/    Per-VU recording and percentile aggregation
examples/            Example test scripts
benchmarks/          Recorded benchmark results
docs/adr/            Architecture decision records
```

See the [architecture decision records](docs/adr/).

## License

[Apache License 2.0](LICENSE)
