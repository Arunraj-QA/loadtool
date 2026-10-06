# Contributing to LoadTool

Thank you for helping. Bug reports, documentation fixes, examples and
code are all welcome.

## Reporting bugs and asking for features

Use the [issue templates](https://github.com/Arunraj-QA/loadtool/issues/new/choose).

**For a bug, include:**

- the smallest script that shows it;
- the command you ran;
- `loadtool --version`, the OS and, for performance issues, the CPU and
  memory.

**Security problems:** report them privately (see [SECURITY.md](SECURITY.md)).

## Building and testing

You need Go (the version in `go.mod`). The race detector also needs cgo
and a C compiler; CI runs it on Linux if you cannot.

```bash
go build -o bin/loadtool ./cmd/loadtool
go test ./...
go vet ./...
go test -race ./...                  # needs cgo
go test -run '^$' -bench . ./...     # benchmarks
gofmt -l .                           # must print nothing
```

**Examples.** Start the benchmark server, then run
`scripts/smoke-examples.sh bin/loadtool`. It runs every local example and
needs `jq`:

```bash
go run ./benchmarks/server &
scripts/smoke-examples.sh bin/loadtool
```

**CI** ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs on
every push:

- gofmt, vet, and tests on Linux and Windows;
- the race detector three times on Linux;
- the release build, the examples and the GitHub Action.

## Making a change

1. **Discuss first** for anything beyond a small fix: open an issue.
   LoadTool is built in phases ([architecture](docs/architecture.md)), and
   features of later phases (distributed execution, dashboards, other
   protocols, …) are deliberately out of scope for now.
2. **Keep changes small and focused.** Use one commit per logical change,
   with conventional prefixes (`feat:`, `fix:`, `test:`, `docs:`, `perf:`,
   `ci:`, `chore:`).
3. **Every change needs tests:**
   - Tests must fail without the change.
   - They must be deterministic. Synchronize on events, not timers: a
     loaded CI runner is much slower than your machine.
4. **Performance-sensitive code needs benchmarks.** That covers the
   request path, the iteration loop, metrics and per-VU memory. Report
   before/after numbers in the pull request, measured the same way, and
   don't claim a speed-up without them.
5. **Record significant design decisions.** Write an ADR in
   `docs/decisions/ADR-NNN-short-name.md`, using the next free number, and
   add it to the [index](docs/decisions/README.md).
6. **Update the docs** that describe the behaviour you changed.

## Where things are

```text
cmd/loadtool/         CLI entry point
internal/cli/         Flags, output, exit codes
internal/runner/      One test run: script, options, setup, VUs, teardown, thresholds
internal/config/      Options, scenarios and their validation
internal/engine/      Goroutine-per-VU scheduler and executors
internal/script/      Script loading (esbuild) and per-VU goja runtimes
internal/httpclient/  HTTP/1.1 and HTTP/2 requests, cookie jars
internal/metrics/     Histograms, checks, time series
internal/thresholds/  Threshold parsing and evaluation
internal/report/      Console summary, JSON summary, HTML report
types/                TypeScript declarations for scripts
examples/             Example scripts
benchmarks/           Benchmark target server, harness and recorded results
docs/                 User documentation, architecture, ADRs
```

## License

By contributing you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
