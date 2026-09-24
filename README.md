# LoadTool

LoadTool is an open-source API performance and load-testing engine written
in Go. Test scenarios are written in TypeScript and run by a Go engine that
uses one goroutine per virtual user (VU).

> **Status: early development (Phase 0).** The CLI skeleton exists, but
> `loadtool run` does not run tests yet.

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
./bin/loadtool --help
./bin/loadtool run script.ts   # currently exits 1: "not implemented yet"
```

## Development

```bash
go test ./...
go vet ./...
go test -race ./...
go test -bench=. -benchmem ./...
```

The `-race` check needs cgo and a C compiler.

## Repository layout

```text
cmd/loadtool/      CLI entrypoint
internal/cli/      Cobra commands (argument parsing and output only)
benchmarks/        Recorded benchmark results
docs/adr/          Architecture decision records
```

Engine, runtime, HTTP and metrics packages will be added under `internal/`
as they are implemented. See
[ADR 0001](docs/adr/0001-cli-and-repository-layout.md).

## License

[Apache License 2.0](LICENSE)
