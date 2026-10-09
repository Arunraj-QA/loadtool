# Changelog

Notable changes, newest first. LoadTool has not published a release
yet; versions follow [semantic versioning](https://semver.org) once it
does.

## Unreleased

### Phase 2: protocol breadth (in progress)

- **WebSocket** (`loadtool/ws`, ADR-019):
  - connect, send, receive and close, with handlers and timers in the
    session;
  - per-message reply latency (`send(…, { reply: true })`);
  - `ws_*` metrics in the summary, the JSON and HTML reports and
    thresholds.
  - It can be mixed with HTTP in one iteration
    (`examples/websocket.ts`), and the demo API has a `/ws/echo`
    endpoint.
  - A blocking style for request/reply tests: `ws.connect(url)` returns a
    socket with `send`, `receive(timeout)` and `close`
    (`examples/websocket-request-reply.ts`).
  - The module can also be imported as `loadtool/websocket`.
- **gRPC** (`loadtool/grpc`, ADR-020), on grpc-go:
  - unary calls and server, client and bidirectional streams (blocking);
  - methods described by `.proto` files (parsed in Go, no `protoc`) or
    by server reflection;
  - metadata and deadlines;
  - `grpc_*` metrics in the summary, reports and thresholds.
  - The demo API serves a greeter service on port 8091, and there are two
    examples (`grpc-unary.ts`, `grpc-streaming.ts`).
- **GraphQL** (`loadtool/graphql`, ADR-021), built on the HTTP
  transport (the same connections, HTTP/2 and cookies; no second HTTP
  client):
  - queries, mutations, variables, headers and a `Client` with default
    headers;
  - results separate HTTP success (`http_ok`) from GraphQL success (`ok`),
    so HTTP 200 with GraphQL errors is a failed operation;
  - `graphql_*` metrics (not `http_*`).
  - The demo API serves `/graphql`, and there are three examples.
- **Kafka** (`loadtool/kafka`, ADR-022), on franz-go:
  - `Producer` (`produce`, `produceBatch`) and `Consumer` (`consume`),
    one client per VU, reused across iterations and closed at the end;
  - keys, headers, explicit partitions, consumer groups, `startAt`;
  - results with `ok`, `error` and `error_code`, never exceptions;
  - `kafka_*` metrics: produce duration, end-to-end consume latency,
    messages produced and consumed, failure rates.
  - The demo API runs an in-process broker on port 9092, there are four
    examples, and CI also tests against a real broker in Docker
    (`testenv/kafka`).
- **Protocol modules** (ADR-014 to ADR-018): a common interface and
  lifecycle for protocols, with metric families (ADR-015) and
  normalized error codes (ADR-016).

### Phase 1: core engine

**Script API**
([Script API](docs/script-api.md), ADR-005, ADR-007, ADR-008, ADR-009, ADR-010, ADR-013):

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
- **Response bodies are discarded by default** (ADR-013), as in Phase 0.
  - To read them, set `discardResponseBodies: false`, or
    `responseType: "text"` in one request's params.
  - Reading a discarded body warns, and `res.json()` throws a
    `TypeError` that says how to keep it.
  - Keeping every body had cost about 2 GB at 1,000 VUs with 1 MB
    responses; see `benchmarks/results/2026-10-07-response-bodies/`.
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

- `--out json` (stdout) and `--out json=<file>`: a versioned JSON summary
  with an `outcome` (passed, exit code, reasons) and a published JSON
  Schema. `--summary-json <file>` is an alias.
- `--report-html`: a self-contained HTML report with per-second charts.
  It shows the run's verdict, exit code and reasons (the same as the JSON
  `outcome`), a throughput card and an error-rate chart. There is an
  example report in `examples/reports/`.
- The console summary shows checks, thresholds, scenarios and dropped
  iterations.

**CI and releases** ([CI](docs/ci/README.md)):

- A GitHub Action (`action.yml`):
  - `version: source` builds LoadTool from the action's checkout, so it
    works before any release;
  - each failure reason becomes an error annotation.
- An example workflow (`.github/workflows/load-test-example.yml`): a
  deterministic test that runs in this repository.
- `scripts/loadtool-ci.sh`, the generic CI recipe the Action also uses.
- `scripts/ci-local.sh`, which reproduces the CI load-test checks
  locally.
- Recipes for GitLab CI, Jenkins and Azure Pipelines.
- Release builds for Linux, macOS and Windows (amd64, arm64) with
  checksums.

**Examples and documentation** ([Examples](examples/README.md),
[docs](docs/README.md)):

- A demo API (`examples/server`): products, JSON orders, a login with a
  bearer token and a session cookie, HTTP/1.1 and h2c.
- Ten examples, all run in CI against the demo API:
  - GET and POST JSON;
  - token and cookie logins;
  - checks and thresholds;
  - all three executors;
  - HTTP/2.
- A troubleshooting guide and a topic index.

**Fixes found along the way:**

- An `async` default function (or `setup`, `teardown`, or a scenario
  function) hid every error: its rejected Promise was ignored, so a
  failing script reported no script errors. Errors in `async` functions
  are now reported, and one that awaits something that never resolves is
  a script error that says so.

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
