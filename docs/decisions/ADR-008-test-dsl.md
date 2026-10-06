# ADR-008: Test DSL — responses, checks, thresholds, scenarios and lifecycle

- Status: Accepted (2026-10-04; implemented in Phase 1 steps 5–7)
- Date: 2026-10-04

## Context

Phase 1 needs a complete test-as-code DSL. The script runtime is already
in place:

| What exists | Decided in |
|---|---|
| goja runtime per VU, esbuild bundling | ADR-001 |
| k6-shaped API | ADR-005 |
| `export const options` with precedence | ADR-006 |
| Built-in modules, `__ENV`, `console`, `sleep`, `group`, lazy built-ins | ADR-007 |

Still missing:

- Response access: bodies are discarded and headers are not exposed.
- Checks.
- Thresholds.
- Scenarios. There is one fixed "N VUs for D seconds" model.
- `setup` and `teardown`.

This ADR fixes the shape of all of them together, so they fit each other.
The API stays small: every feature listed is needed for a realistic API
test, and anything else is left out (see "Not in Phase 1").

**Constraints:**

- **Extend, don't rewrite.** The Phase 0 runtime, engine, metrics and
  report are extended.
- **Per-VU memory is the Phase 0 exit criterion.** New built-ins must be
  lazy (ADR-007), and per-request work must avoid retained allocations.
- **Out of scope here:** HTTP/2, JSON/HTML output and CI integration.

## Decision

### 1. Script shape

```typescript
import http from "loadtool/http";
import { check, sleep } from "loadtool";

export const options = {
  scenarios: {
    browse: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "30s", target: 50 },
        { duration: "1m", target: 50 },
        { duration: "10s", target: 0 },
      ],
    },
    orders: {
      executor: "constant-arrival-rate",
      rate: 20, timeUnit: "1s", duration: "1m",
      preAllocatedVUs: 30,
      exec: "placeOrder",
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<500", "p(99)<1000"],
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
  },
};

export function setup() {
  const res = http.post(`${__ENV.BASE}/login`, JSON.stringify({ user: "load" }),
    { headers: { "Content-Type": "application/json" } });
  return { token: res.json().token };
}

export default function (data) {
  const res = http.get(`${__ENV.BASE}/products`,
    { headers: { Authorization: `Bearer ${data.token}` } });
  check(res, {
    "status is 200": (r) => r.status === 200,
    "has products": (r) => r.json().length > 0,
  });
  sleep(1);
}

export function placeOrder(data) { /* ... */ }

export function teardown(data) { /* ... */ }
```

Every name and shape here exists in k6 with the same meaning (ADR-005).
LoadTool supports a subset; anything outside it is rejected or warned
about, never silently changed.

### 2. Runtime API

**`loadtool/http`.** The http module object is lazy (ADR-007), and named
exports are marked pure so unused ones are tree-shaken. A VU pays only for
the methods its script uses.

| Call | Notes |
|---|---|
| `get(url, params?)` | |
| `post(url, body?, params?)` | `put`, `patch` and `del` take the same arguments |
| `request(method, url, body?, params?)` | |
| `body` argument | A string. An object is a `TypeError` that suggests `JSON.stringify`. k6 form-encodes objects, which is easy to get wrong silently, so LoadTool refuses instead. |
| `params` | `{ headers: { name: value } }`. Other keys produce a warning once per run. |

**Response object:**

| Field | Value |
|---|---|
| `status` | Number; 0 on transport failure |
| `error` | `""` or the transport error message |
| `headers` | `{ "Content-Type": "..." }` with canonical names; repeated headers are joined with `", "` |
| `body` | String; `null` when bodies are discarded |
| `json()` | Parses `body`. Throws on invalid JSON or a discarded body |
| `timings.duration` | Milliseconds |
| `url` | Final request URL |

**`loadtool`:** `check`, `sleep`, `group` (ADR-007).

**Options:**

| Key | Meaning |
|---|---|
| `vus`, `duration` | Existing (ADR-006). Shorthand for one `constant-vus` scenario named `default`. |
| `stages` | Shorthand for one `ramping-vus` scenario named `default`. |
| `scenarios` | Section 6. |
| `thresholds` | Section 5. |
| `discardResponseBodies` | `false` by default, as in k6. The benchmark scenario sets it to `true` to match the k6 scenario. |
| `setupTimeout`, `teardownTimeout` | Default `"60s"`. |

### 3. How JavaScript maps to Go

| JavaScript | Go | Conversion |
|---|---|---|
| `options` | `config.Options` struct | `JSON.stringify` in the VU-0 runtime, then `encoding/json` into typed structs (ADR-006). Scenarios and thresholds are validated in Go before any VU starts. |
| Exported functions (`default`, `setup`, `teardown`, `exec` targets) | `goja.Callable` per VU | esbuild metadata lists the script's exports. The generated entry binds only the ones used, as direct references, so there are still no interop helpers. A missing `exec` target is an error before the test starts. |
| `setup()` return value | `[]byte` (JSON) | Stringified once, then parsed once per VU at VU start. Every iteration of that VU gets the same object. Functions and `undefined` are dropped, as in `JSON.stringify`. |
| Request `params` | `httpclient.Request` | Read property by property; no reflection. |
| Response | goja dynamic object over the Go result | `headers`, `body`, `timings` and `json` are built on first access, so a script that only reads `status` builds none of them. The body is kept as `[]byte` and becomes a JS string only when `body` or `json()` is used. Measured in step 5a: 80 allocations per HTTP iteration, down from 88 with the eager Phase 0 object, even though bodies are now kept. |
| `check(value, sets)` | Native function | Calls each condition through `goja.AssertFunction`. The result is `ToBoolean()`. |
| Check names | Index into a per-run name table | Interned on first use. Per-VU counters are plain integers, merged at the end like the existing recorders. |

### 4. Checks

- `check(value, { name: (v) => boolean })` runs every condition in order
  and returns `true` only if all pass.
- **A condition that throws counts as failed**, not as a script error. The
  first error message per check is kept for the summary.
  - Rationale: `(r) => r.json().id` against an HTML error page should fail
    the check, not end the iteration.
  - Non-function conditions are a `TypeError`: a script bug, reported
    as a script error.
- A failed check does not stop the iteration or count as a request error.
- **Results:**
  - Each VU's recorder counts pass/fail per check name, without locks; the
    run merges them at the end.
  - The summary lists each check with its pass rate, in first-seen order.
  - The `checks` metric is passes ÷ all check runs, across all names.

### 5. Thresholds

```typescript
thresholds: { metric: ["<aggregate> <op> <number>", ...] }
```

**Metrics:**

| Metric | Meaning | Aggregates |
|---|---|---|
| `http_req_duration` | ms, all sent requests (same population as k6) | `avg`, `min`, `max`, `med`, `p(N)` |
| `http_req_failed` | failed ÷ requests | `rate` |
| `http_reqs` | requests | `count`, `rate` (per second) |
| `checks` | passed ÷ checks | `rate` |
| `iterations` | completed iterations | `count`, `rate` |
| `dropped_iterations` | arrival-rate starts with no free VU | `count`, `rate` |

**Expressions:**

- Operators are `<`, `<=`, `>`, `>=`, `==` and `!=`.
- Expressions are parsed in Go when options are read. A typo is an error
  before any load starts.
- Unknown metric names are errors. So are object forms
  (`{ threshold, abortOnFail }`) and sub-metrics (`http_req_duration{...}`),
  with messages saying they are not supported yet.

**Evaluation:**

- Thresholds are evaluated **once, at the end**, against the merged
  `metrics.Summary`. They consume the same numbers the summary prints,
  so the two always agree.
- A metric with no samples (for example no checks ran) **fails** its
  thresholds and shows "no data", rather than passing silently.
- Percentiles are within ±0.78 % (ADR-004). A result that close to the
  limit is marked "≈" in the summary.

**Reporting:** the summary shows ✓ or ✗ per expression with the observed
value. If any threshold fails, the exit code is **99**, the same as k6, so
CI recipes carry over.

**Details settled when thresholds were implemented (2026-10-06):**

- `iterations` counts iterations that returned before the test's
  context ended, script errors included. Iterations cancelled at the end
  of the graceful stop or by Ctrl+C are not counted.
- **Exit code precedence.**
  - An interrupted run or a failed teardown exits 1, even if thresholds
    also failed: partial metrics should not be reported as a threshold
    verdict.
  - 99 means the test ran fully and only thresholds failed.
- Thresholds are parsed after the other options and before setup, so a
  bad expression never runs setup or any load.

### 6. Scenarios and executors

Three executors cover the roadmap:

| Executor | Fields | Behaviour |
|---|---|---|
| `constant-vus` | `vus`, `duration` | Today's engine loop |
| `ramping-vus` | `startVUs`, `stages[{duration, target}]`, `gracefulRampDown` (30s) | A controller raises or lowers the number of *active* VUs linearly within each stage. Deactivated VUs finish their iteration, waiting up to `gracefulRampDown`, then park. |
| `constant-arrival-rate` | `rate`, `timeUnit` (1s), `duration`, `preAllocatedVUs` | Iterations start on a fixed schedule computed from the start time, so the rate does not drift. Each start is handed to an idle VU. If none is idle, the start is counted in `dropped_iterations` and not queued. |

Fields every executor accepts:

- `exec`: the exported function to run; default `default`.
- `startTime`: default `0s`.
- `gracefulStop`: default `30s`.

**VU allocation:**

- Each scenario gets its own VUs: its maximum for `ramping-vus`, or
  `preAllocatedVUs`.
- All VUs are created and run their init code **before the clock starts**,
  as today. Memory is therefore known up front.
- `__VU` numbers are unique across scenarios.
- `maxVUs` (growing an arrival-rate pool during the run) is **not**
  supported in Phase 1. A value above `preAllocatedVUs` is an error that
  says so, rather than being silently ignored.

**Precedence (ADR-006):**

- A typed `--vus`/`--duration` replaces all scenarios with one
  `constant-vus` scenario, and the CLI says so.
- Options that set both `scenarios` and a shorthand (`vus`, `duration` or
  `stages`) are an error, because the meaning is ambiguous.
- `--graceful-stop` sets the shorthand scenario's `gracefulStop`.

**Engine:**

- `engine.Run` keeps its goroutine-per-VU model.
- Executors decide *when* a VU starts an iteration: always, gated by an
  active flag, or on a schedule.
- `Run` takes a list of scenarios, starts each at its `startTime`, and
  returns when every scenario has finished its graceful stop.

**Details settled when scenarios were implemented (2026-10-06):**

*Ramping and arrival-rate behaviour:*

- **Activation.** In `ramping-vus`, VU *i* (from 0) is active while the
  stage line is at least *i*+1.
  - Activation and deactivation times are computed exactly, in integer
    microseconds. A property test checks this against the definition.
  - A pure ramp reaches its last VU only as the stages end, so scripts need
    a hold stage to run every VU.
  - A zero-length stage is a jump. Several jumps at one instant leave the
    last in force.
- **No controller goroutine.** Each ramping VU sleeps until it next becomes
  active. Its iteration gets a deadline of removal + `gracefulRampDown`
  only when a removal falls inside the scenario.
- **The arrival-rate scheduler** computes start *k* as
  `start + k·timeUnit/rate`. It hands starts over an unbuffered channel
  with a non-blocking send, so a start reaches an idle VU or is dropped.
  Its goroutine is joined like the VUs'.

*Options and precedence:*

- **The `stages` shorthand** starts from `vus` (default 1), as in k6.
  Combining `stages` with `duration` is an error.
- **Replacing scenarios.** `LOADTOOL_VUS`/`LOADTOOL_DURATION` replace
  scenarios as well as typed flags: they rank above script options
  (ADR-006). The runner warns when that happens.
- **Fields of another executor** are errors, not ignored.
- **`maxVUs`** is accepted only when it equals `preAllocatedVUs`.
- **Scenario order and `__VU`.** Scenarios are ordered by name, and `__VU`
  numbers follow that order.

*`exec` functions:*

- **How they are bound.** After options are read, the script is compiled
  once more with an entry that binds only the named `exec` functions. The
  names are checked against esbuild's export list first. Each VU picks its
  function through the lazy `exec` built-in, so no per-VU global is added:
  retained memory per VU went from 4,640 B to 4,661 B, the new field.
- **`default` is needed only if a scenario runs it.** That is checked in
  the lifecycle runtime before setup.

*Unchanged:*

- **The start-up error** keeps its 0-based VU index ("initialize VU 3")
  from Phase 0.

### 7. Lifecycle

1. **Load:** bundle the script and read its exports.
2. **Init + options:** the VU-0 runtime runs top-level code, and options
   are resolved and validated. Any error stops here (exit 1).
3. **Setup:** if `setup` is exported, it runs once in the VU-0 runtime.
   - HTTP is allowed. Its requests are **not** counted in the test
     metrics, so a login call cannot affect thresholds.
   - Bounded by `setupTimeout`.
   - If it throws or times out, there is no load phase and no teardown,
     and the exit code is 1.
4. **VU init:** every scenario's VUs are created; top-level code runs per
   VU, and setup data is parsed per VU.
5. **Load phase:** scenarios run, and each iteration calls `exec(data)`.
6. **Teardown:** if `teardown` is exported, it runs once in the VU-0
   runtime with the setup data.
   - It runs whether thresholds passed or not.
   - It also runs after Ctrl+C, bounded by `teardownTimeout`. A second
     Ctrl+C exits at once (existing behaviour).
   - Its requests are not counted.
7. **Thresholds** are evaluated and the summary is printed.

The default function receives `data` (or `undefined` without `setup`).
Mutations to `data` stay inside that VU.

**Details settled when the lifecycle was implemented (2026-10-06).**

*Scope and state:*

- **One lifecycle runtime.** Top-level code, options, `setup` and
  `teardown` all use one VU-0 runtime (`script.Lifecycle`), so module
  state that setup sets is visible to teardown. No VU runs JavaScript
  while setup or teardown does: setup ends before the first VU is created,
  and teardown starts after `engine.Run` has waited for every VU
  goroutine.
- **Only VU 0 holds `setup` and `teardown`.** The generated entry stores
  them only when `__VU === 0`. Exporting them costs other VUs nothing
  (retained memory per VU is unchanged, measured with
  `BenchmarkVURetainedMemory`).
- **Data.**
  - Setup's return value becomes JSON once. Each VU parses it once when
    created.
  - `teardown` parses the same JSON, so it sees exactly what the VUs saw.
  - A value `JSON.stringify` rejects (a cycle, a BigInt) fails setup.
  - `undefined` or a function means no data.
- **Results not counted.** `setup` and `teardown` record into a recorder
  that is thrown away, which covers both requests and checks.
- **Response bodies are always kept** in `setup` and `teardown`, whatever
  `discardResponseBodies` says, because setup typically reads a token.

*When teardown runs:*

- **Rule:** teardown runs if and only if setup completed, or the script
  has no setup.
- That includes VU start-up failing after setup. The start-up error and
  any teardown error are then returned together.
- It includes a load phase stopped by Ctrl+C.
- It never runs after a failed or interrupted setup.

*Cancellation:*

- Ctrl+C during setup stops setup and ends the test.
- A phase counts as interrupted when the test was cancelled by the time
  it returned, even if the function returned normally. `sleep` returns
  early on cancellation and the interrupt arrives asynchronously, so the
  JavaScript may finish first. Before this rule (found by a stress run,
  2026-10-06), such a setup counted as successful and the runner went on
  to start VUs and teardown after Ctrl+C.
- After a Ctrl+C that stopped the load phase, teardown runs on a context
  that ignores that cancellation, bounded by `teardownTimeout`. A second
  Ctrl+C ends the process.
- A Ctrl+C that arrives during teardown (after a completed load phase)
  stops teardown. That is reported as a teardown failure; the load phase
  is not marked interrupted.
- When a timeout or Ctrl+C fires just as setup returns, goja keeps the
  runtime's interrupt flag set and would abort teardown at once. The
  lifecycle waits for the interrupt to fire and then clears it.

*Failures:*

- **A setup failure** is an error with no result (exit 1). It covers a
  throw, `setupTimeout`, Ctrl+C or unserializable data.
- **A teardown failure** never replaces the result. It is returned in
  `report.Result.TeardownError`, shown in the summary after the status
  line, and makes the CLI exit 1 after printing the full summary.

### 8. Error handling

| Where | What happens | Exit code |
|---|---|---|
| Bundle/import/syntax error | Message with `file:line` | 1 |
| Invalid options: bad executor, threshold expression, unknown metric, missing `exec`, `maxVUs` | Message naming the scenario/metric and the source (ADR-006) | 1 |
| Unknown option keys | Warning, run continues (ADR-006) | — |
| Top-level code throws | Error before load | 1 |
| `setup` throws or times out | Error, no load phase | 1 |
| Iteration throws | Counted under "Script errs", the iteration ends and the VU continues | — |
| HTTP transport failure | No throw: `status: 0`, `error` set, counted in `http_req_failed` | — |
| Check condition throws | The check fails and the first message is kept | — |
| `res.json()` on invalid JSON | Throws `SyntaxError` (inside a check, the check fails) | — |
| `teardown` throws | Error printed after the summary | 1 |
| Ctrl+C | Load stops, teardown runs, partial summary | 1 |
| Thresholds failed (and nothing above) | ✗ lines in the summary | 99 |

## Not in Phase 1

- Custom metrics (`Counter`, `Trend`, ...).
- Tags and sub-metric thresholds.
- `abortOnFail`.
- Executors `per-vu-iterations`, `shared-iterations` and
  `ramping-arrival-rate`, and `maxVUs`.
- Object or form request bodies, file uploads, `http.batch`.
- Cookies (step 8, Sessions).
- HTTP/2 (step 9).
- JSON/HTML output (steps 10–11).
- CI integration (step 12).

## Consequences

- Scripts written for this subset of k6 run unchanged apart from the
  import paths (ADR-005).
- **Response bodies are read by default.** This adds per-request
  allocations: the body bytes, plus a string if the script reads it.
  `discardResponseBodies: true` restores Phase 0 behaviour and is used by
  the benchmark scenario.
- **Retained memory per VU** must stay near the ADR-007 figures.
  `check`, `post` and the rest are lazy, and responses build their fields
  only when read. Setup data is copied into every VU, so large setup data
  costs size × VUs; this is documented.
- **The engine gains executors.** The constant-VU path must keep its
  Phase 0 performance; this is measured A/B against the step 4 binary.
- **Exit codes become part of the CLI contract:** 0, 1 and 99.
