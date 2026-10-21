# ADR-023: Local UI for testing one HTTP API

- Status: Proposed (2026-10-21, Phase 2)
- Date: 2026-10-21
- Builds on: ADR-008 (lifecycle), ADR-009 (sessions and connections),
  ADR-012 (time series), ADR-013 (response bodies)

## Context

Phase 2 exit criterion 5, added on 2026-10-21 at the user's request: a
user enters one publicly hosted HTTP API in a UI, configures a load test,
runs it with the existing engine and sees the results in a dashboard,
without writing a TypeScript script.

What the code offers today (checked at `0b8f9b4`):

- **Runner:** `runner.Run(ctx, Params)` runs a test from a **script
  file** and returns a `report.Result` when the test ends. It has no
  progress callback. `Params.Env` becomes the script's `__ENV` and is
  never written to reports.
- **HTTP client:** one transport shared by all VUs. Redirects are never
  followed: a 3xx response is the result. The transport is cloned from
  `http.DefaultTransport`, so it honours proxy environment variables and
  dials any address. Every request has a fixed 30 s timeout.
- **Metrics:** latency histograms, checks and a 1 s time series. No
  HTTP status-code counts.
- **Reports:** `report.JSON` and `report.HTML`. They include the script
  path and error messages.
- **No UI, HTTP server or frontend dependency** outside the demo API.

## Decision

### 1. A loopback controller, `loadtool ui`

- A new subcommand serves a single page and a small JSON API with the
  standard library's `net/http`. The page (HTML, CSS and plain
  JavaScript) is embedded in the binary. **No new dependencies.**
- It listens on `127.0.0.1` only.
- It runs **one test at a time** through `runner.Run`. The browser only
  sends a configuration and reads status; it never generates load.

### 2. A fixed script, configured through the environment

- The controller runs one **fixed TypeScript script embedded in the
  binary**. It reads the request and the thresholds from `__ENV` (JSON
  values the controller builds), sends the request with `http.request`,
  and checks the status code. User input is never turned into code.
- **Request headers, credentials included, travel only in `Params.Env`**,
  which is never written to reports.
- The script never throws, so request URLs do not reach
  `FirstScriptError`.
- The script is written to a temporary file for the run (the runner
  loads scripts from files) and removed afterwards.
- The reports' script field is set to a neutral label (the method and
  the URL without its query), not the temporary path.

### 3. Destination guard (the public-API workflow)

- **Before a run:** the URL must be absolute `http` or `https`, with a
  host and no user info.
- **At every connection:** the dialer checks the **resolved IP**. It
  refuses loopback, private (RFC 1918), shared (100.64.0.0/10),
  link-local (169.254.0.0/16, cloud metadata included), IPv6 unique-local
  and link-local, unspecified, multicast and other reserved ranges.
  Checking at connect time also covers DNS rebinding and every new
  connection during the test.
- **The proxy environment is ignored** for UI runs, so a proxy cannot
  bypass the check.
- **Redirects** need no extra check: they are not followed.
- `--allow-private-targets` turns the guard off, with a warning, for
  local servers such as the demo API.
- Added as **default-off options** in `httpclient.Options`
  (`DialControl`, `NoProxy`) and `runner.Params`. `loadtool run` does
  not set them, so it is unchanged.

### 4. Limits

- VUs capped at 50 and duration at 5 minutes by default (`--max-vus`,
  `--max-duration`).
- A request timeout of 10 s for UI runs (`--request-timeout`), through a
  new `runner.Params.RequestTimeout` (0 keeps the 30 s default).
- JSON request bodies up to 256 KB, and controller requests up to 1 MB.
- The user must confirm they are authorized to load-test the API.

### 5. HTTP status codes in the metrics

- Status codes are counted per code, in the **shared histogram shards**
  (16 shards with a fixed table of atomic counters): one atomic increment
  per response, like the latency histograms, and **no per-VU memory**
  (Phase 2 exit criterion 3).
- `Summary.StatusCodes` (non-zero codes only) is added to the console
  summary, and additively to the JSON summary (`statusCodes`) and the HTML
  report.

### 6. Live progress

- `runner.Params.OnSample func(metrics.Point)` receives each 1 s
  time-series point as the engine samples it. It is optional (nil keeps
  today's behaviour) and must return quickly; the controller only
  appends to a buffer.
- **The dashboard computes no metrics.** Live values come from these
  points; final values come from the run's `report.JSON` output, the same
  source as the reports.

### 7. Controller security

- **Host** must be `127.0.0.1:<port>` or `localhost:<port>` (DNS
  rebinding).
- **Every API call** carries a per-session token in a header. The token
  is printed once, in the URL fragment, which the browser never sends.
- **`Origin`**, when present, must be the controller's own. There are
  no CORS headers, and `POST` bodies must be JSON.
- **Secrets:** masked in the page and never returned by the API. Every
  string the controller shows or logs (errors, report errors, preflight
  output) has secret values replaced with `***`.

## Consequences

- **`loadtool run`, HTTP/1.1 and HTTP/2, scenarios, checks, thresholds,
  reports and the protocol modules are unchanged.** The new runner,
  HTTP-client and engine inputs are optional and off by default.
- **One change touches the HTTP recording path:** counting status codes.
  It is checked with the HTTP micro-benchmarks and the criterion 2
  regression run.
- **The JSON summary gains `statusCodes`.** It is additive; the schema
  is updated, and `schemaVersion` stays the same.
- **Live percentiles are per second.** Overall percentiles appear when
  the run ends, from the merged histogram.
- **The page is deliberately small.** It is not the later-phase
  analytics dashboard: there is no history, storage, comparison or
  multi-user access.

## Alternatives considered

- **A JavaScript framework (for example React) with a build step:**
  rejected. There is no frontend in the repository, it would add
  dependencies and tooling, and CLAUDE.md defers a React dashboard to a
  later phase.
- **Generating TypeScript from the form:** rejected. Escaping user input
  into code is error-prone; a fixed script with JSON configuration is
  not.
- **A Go iteration function that bypasses the script engine:** rejected.
  It would be a second way to generate load, outside the DSL's checks
  and thresholds.
- **Load generated by the browser:** rejected by the requirements.
- **Status codes as one check per code in the script:** rejected. It
  pollutes `checks` and the `checks` threshold.
- **Checking destinations only before the run:** rejected. DNS can
  change between the check and the connection (rebinding).
