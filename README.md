# LoadTool

LoadTool is an open-source load-testing tool for HTTP APIs. You write
tests in TypeScript or JavaScript, with an API shaped like
[k6](https://k6.io)'s, and a Go engine runs them, one goroutine per
virtual user.

```typescript
import http from "loadtool/http";
import { check, sleep } from "loadtool";

export const options = {
  stages: [
    { duration: "30s", target: 50 },
    { duration: "2m", target: 50 },
  ],
  thresholds: { http_req_duration: ["p(95)<300"], http_req_failed: ["rate<0.01"] },
};

export default function () {
  const res = http.get(`${__ENV.BASE_URL}/api/products`);
  check(res, { "status is 200": (r) => r.status === 200 });
  sleep(1);
}
```

```bash
loadtool run -e BASE_URL=https://staging.example.test test.ts --report-html report.html
```

> **Status: early development.** Phase 1 (the core engine) is close to
> complete, and no release has been published yet. Build from source to
> try it, and expect changes. See [Project status](#project-status).

## Features

**Test as code:**

- TypeScript or JavaScript with your own modules and JSON data files.
- `check` for assertions.
- `setup`/`teardown` around the test.
- `__ENV` for per-environment settings.

**Workloads:**

- Constant VUs, ramping VUs, or a constant arrival rate.
- Several scenarios in one test, each running its own function.

**Pass/fail:**

- Thresholds on latency percentiles, error rate, checks and more.
- Exit code 99 when a threshold fails, so CI jobs fail.

**HTTP:**

- HTTP/1.1 and HTTP/2 (including h2c), with connection reuse.
- A cookie session per VU.

**Results:**

- A console summary.
- A versioned JSON summary.
- A self-contained HTML report with charts over time.

**CI:**

- A GitHub Action.
- Recipes for GitLab CI, Jenkins and Azure Pipelines.

**Efficient:**

- Fixed-size latency histograms, so memory does not grow with test
  length.
- About 5 KB of retained script heap per VU, measured without goroutine
  stacks and connections (`BenchmarkVURetainedMemory`).
- In the Phase 0 benchmark at 1,000 VUs, LoadTool used less memory than
  JMeter; see [Benchmarks](#benchmarks).

## Install

**From source.** Requires Go (see `go.mod`):

```bash
git clone https://github.com/Arunraj-QA/loadtool.git
cd loadtool
go build -o bin/loadtool ./cmd/loadtool
```

**Releases** will be published as static binaries for Linux, macOS and
Windows on the [releases page](https://github.com/Arunraj-QA/loadtool/releases).

## Quick start

```bash
go run ./benchmarks/server &                       # a local test target
./bin/loadtool run examples/checks.ts --vus 5 --duration 10s
```

Then read [Getting started](docs/getting-started.md).

## Documentation

| | |
|---|---|
| [Getting started](docs/getting-started.md) | A first test, checks, thresholds, result files |
| [Script API](docs/script-api.md) | Requests, responses, checks, lifecycle, cookies, HTTP/2 |
| [Options](docs/options.md) | All options, scenarios, thresholds |
| [Command line](docs/cli.md) | Flags, exit codes, environment |
| [Results](docs/results.md) | What the numbers mean |
| [CI](docs/ci/README.md) | GitHub Action and other CI systems |
| [Differences from k6](docs/k6-differences.md) | Porting k6 scripts |
| [Examples](examples/README.md) | Runnable scripts |

All documentation: [docs/](docs/README.md).

## Project status

LoadTool is built in phases:

- **Phase 0 (complete):** the foundation. A goroutine-per-VU HTTP/1.1
  engine, TypeScript execution, a console summary.
- **Phase 1 (in progress):** the core engine. Everything under
  [Features](#features) is done. A benchmark re-run and the Phase 1 exit
  criteria remain.

**Planned for later phases:** distributed execution, more protocols,
real-time dashboards, browser testing, a test recorder and AI-assisted
analysis.

[docs/architecture.md](docs/architecture.md) has the details.

## Benchmarks

Performance claims are only made from recorded measurements, kept in
[benchmarks/](benchmarks/README.md).

**The Phase 0 comparison** (2026-10-01) ran with one laptop running both
the tools and the target server, and JMeter on its default JVM settings.
At 1,000 VUs, LoadTool's peak private memory was 190–210 MB against
JMeter's 1,366–1,371 MB on every run. See
[the full results](benchmarks/results/2026-10-01-phase0-all-tools/),
including k6.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: see
[SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)
