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
| Response | `goja.Object` | Built per request with a per-VU prototype that holds `json()`, so no function object is created per response. The body is kept as `[]byte` and becomes a JS string only when `body` or `json()` is used. |
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
  `check`, `post` and the rest are lazy, and the response prototype is one
  object per VU. Setup data is copied into every VU, so large setup data
  costs size × VUs; this is documented.
- **The engine gains executors.** The constant-VU path must keep its
  Phase 0 performance; this is measured A/B against the step 4 binary.
- **Exit codes become part of the CLI contract:** 0, 1 and 99.
