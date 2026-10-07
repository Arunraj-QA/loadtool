# Changelog

Notable changes, newest first. LoadTool has not published a release
yet; versions follow [semantic versioning](https://semver.org) once it
does.

## Unreleased

### Phase 1: core engine

**Script API**
([Script API](docs/script-api.md), ADR-005, ADR-007, ADR-008, ADR-009, ADR-010):

- **Breaking for Phase 0 scripts:** `http` is now imported
  (`import http from "loadtool/http"`) instead of being a global. The
  error message says so.
- **Modules and globals:**
  - Built-in modules `loadtool/http` and `loadtool`.
  - Relative imports of scripts and JSON files.
  - `__ENV`, `__VU`, `__ITER`, `console`.
- **HTTP:**
  - `http.post`, `put`, `patch`, `del` and `request`.
  - Responses with headers, body, `json()`, cookies and the protocol.
- **Assertions and structure:** `check`, `sleep`, `group`; `setup` and
  `teardown`.
- **Sessions:** a cookie jar per VU, reset each iteration
  (`noCookiesReset` keeps it).
- **HTTP versions:** HTTP/2 over TLS by default (`httpVersion`), and h2c
  with `httpVersion: "2"`. Responses are counted by protocol in the
  summary, the JSON summary and the HTML report.

**Options and workloads** ([Options](docs/options.md), ADR-006, ADR-008):

- `export const options`, with precedence: flags, then environment, then
  script.
- Scenarios with the `constant-vus`, `ramping-vus` and
  `constant-arrival-rate` executors; `stages` shorthand; `exec` functions.
- Thresholds on `http_req_duration`, `http_req_failed`, `http_reqs`,
  `checks`, `iterations` and `dropped_iterations`, with exit code 99 on
  failure.
- `discardResponseBodies`, `noConnectionReuse`, `setupTimeout`,
  `teardownTimeout`.

**Results** ([Results](docs/results.md), ADR-011, ADR-012):

- `--summary-json`: a versioned JSON summary.
- `--report-html`: a self-contained HTML report with per-second charts.
- The console summary shows checks, thresholds, scenarios and dropped
  iterations.

**CI and releases** ([CI](docs/ci/README.md)):

- A GitHub Action (`action.yml`).
- Recipes for GitLab CI, Jenkins and Azure Pipelines.
- Release builds for Linux, macOS and Windows (amd64, arm64) with
  checksums.

**Fixes found along the way:**

- A setup cancelled with Ctrl+C could count as successful and start the
  load phase.
- On Windows, replacing a result file another process briefly holds open
  is retried instead of failing.
- The arrival-rate executor could drop a start while VUs were idle, if
  none was waiting at that instant; it counts idle VUs explicitly now.

### Phase 0: foundations

- `loadtool run` with `--vus`, `--duration`, `--graceful-stop`.
- A goroutine-per-VU HTTP/1.1 engine with keep-alive and a graceful stop.
- TypeScript and JavaScript tests run in goja, bundled by esbuild.
- Request, error and latency metrics: p50, p90, p95, p99 from fixed-size
  histograms.
- A console summary.
- A benchmark against k6 and JMeter at up to 1,000 VUs
  ([results](benchmarks/results/2026-10-01-phase0-all-tools/)).
