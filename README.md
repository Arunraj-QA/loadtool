# LoadTool

LoadTool is an open-source API performance and load-testing engine written
in Go. Test scenarios are written in TypeScript and run by a Go engine that
uses one goroutine per virtual user (VU).

> **Status: early development (Phase 0).** `loadtool run` generates HTTP/1.1
> GET load against a URL and prints a summary. The script file must exist
> but is **not executed yet**; goja script execution is the next step.

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
./bin/loadtool run test.ts --url http://localhost:8080/ --vus 100 --duration 30s
```

| Flag | Default | Meaning |
|---|---|---|
| `--url` | (required) | Target requested by every iteration (`GET`) |
| `-u, --vus` | `1` | Concurrent virtual users (one goroutine each) |
| `-d, --duration` | `10s` | How long VUs keep iterating |

Press Ctrl+C to stop early. A partial summary is printed and the exit code is 1.

How results are counted:
- A request succeeds when it gets a 2xx or 3xx response.
- Redirects are not followed.
- Requests cut off by the end of the test are not counted.

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
cmd/loadtool/      CLI entrypoint
internal/cli/        Cobra commands and console summary
internal/config/     Run settings and validation
internal/engine/     Goroutine-per-VU scheduler (protocol-agnostic)
internal/httpclient/ HTTP/1.1 request execution
internal/metrics/    Per-VU recording and percentile aggregation
benchmarks/          Recorded benchmark results
docs/adr/            Architecture decision records
```

See [ADR 0001](docs/adr/0001-cli-and-repository-layout.md) and
[ADR 0002](docs/adr/0002-http-load-generator.md).

## License

[Apache License 2.0](LICENSE)
