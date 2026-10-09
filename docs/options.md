# Options

A script declares its settings in `export const options`:

```typescript
export const options = {
  vus: 10,
  duration: "30s",
  thresholds: { http_req_failed: ["rate<0.01"] },
};
```

- [All options](#all-options)
- [Where settings come from](#where-settings-come-from)
- [Scenarios](#scenarios)
- [Thresholds](#thresholds)

## All options

| Option | Default | Meaning |
|---|---|---|
| `vus` | `1` | VUs of the default scenario |
| `duration` | `"10s"` | How long the default scenario runs |
| `stages` | | Ramping shorthand: `[{ duration: "30s", target: 10 }, ...]` ([Scenarios](#scenarios)) |
| `scenarios` | | Named workloads ([Scenarios](#scenarios)) |
| `thresholds` | | Pass/fail criteria ([Thresholds](#thresholds)) |
| `discardResponseBodies` | `true` | Drop response bodies after reading them; `false` keeps them for `res.body` and `res.json()` ([Script API](script-api.md#responses)) |
| `noCookiesReset` | `false` | Keep each VU's cookies across iterations ([Script API](script-api.md#cookies-and-sessions)) |
| `noConnectionReuse` | `false` | Open a new connection for every request |
| `httpVersion` | `"auto"` | `"auto"`, `"1.1"` or `"2"` ([Script API](script-api.md#http-versions-and-connections)) |
| `setupTimeout` | `"60s"` | Time limit for `setup()` |
| `teardownTimeout` | `"60s"` | Time limit for `teardown()` |

**Formats.** Durations are strings like `"30s"` or `"1m30s"`, or numbers
of milliseconds.

**Unsupported options** produce a warning and are ignored, so a script
written for k6 still runs. The warning names them.

## Where settings come from

`vus` and `duration` can also come from the command line or the
environment. The first source that sets a value wins:

1. a flag you typed: `--vus`, `--duration`
2. an environment variable: `LOADTOOL_VUS`, `LOADTOOL_DURATION`
3. the script's `options`
4. the default (1 VU, 10 s)

A typed `--vus`/`--duration` (or the variables) replaces the script's
`scenarios` or `stages` with one constant-VU scenario, and LoadTool
prints a warning saying so. That is handy for a quick smoke run of any
script.

## Scenarios

**Scenarios describe the workload:** how many VUs run, and when they
start iterations ([`examples/scenarios.ts`](../examples/scenarios.ts)).
All scenarios run at the same time, each from its `startTime`.

```typescript
export const options = {
  scenarios: {
    browsers: {
      executor: "ramping-vus",
      stages: [
        { duration: "1m", target: 50 },  // ramp up
        { duration: "5m", target: 50 },  // hold
        { duration: "1m", target: 0 },   // ramp down
      ],
      exec: "browse",                    // exported function to run
    },
    orders: {
      executor: "constant-arrival-rate",
      rate: 20, timeUnit: "1s", duration: "7m",
      preAllocatedVUs: 40,
      startTime: "30s",
      exec: "placeOrder",
    },
  },
};

export function browse() { /* ... */ }
export function placeOrder() { /* ... */ }
```

| Executor | Fields (defaults) | What it does |
|---|---|---|
| `constant-vus` | `vus` (1), `duration` | `vus` VUs run iterations back to back |
| `ramping-vus` | `startVUs` (1), `stages`, `gracefulRampDown` (`"30s"`) | The number of active VUs moves linearly through the stages |
| `constant-arrival-rate` | `rate`, `timeUnit` (`"1s"`), `duration`, `preAllocatedVUs` | `rate` iterations start per `timeUnit`, however long each takes |

Every scenario also takes:

- `exec`: the exported function to run; default `default`.
- `startTime`: default `"0s"`.
- `gracefulStop`: default `"30s"`. How long running iterations may finish
  when the scenario ends.

**Shorthands:**

- `vus` with `duration` is one `constant-vus` scenario named `default`.
- `stages` is one `ramping-vus` scenario, starting from `vus` (default 1).
- `--graceful-stop` applies to the `vus`/`duration` shorthand only.

**VUs:**

- All VUs of all scenarios are created, and run the top-level code,
  before the clock starts, so memory use is known up front.
- `__VU` numbers are unique, in scenario name order.

**Ramping:**

- VU *n* (from 1) is active while the stage line is at least *n*. A pure
  ramp from 0 to 10 reaches the 10th VU only as it ends; add a hold
  stage to run them all.
- A `"0s"` stage jumps at once.
- A VU removed by a ramp-down may finish its iteration for up to
  `gracefulRampDown`.

**Arrival rate:**

- Starts follow a fixed schedule, so the rate does not drift.
- If no VU is free, the start is **dropped**, not queued. The summary
  shows "Dropped: N iterations" and the `dropped_iterations` metric
  counts them. Raise `preAllocatedVUs` when that happens.
- `maxVUs` (growing the pool during the test) is not supported yet.

**Validation:**

- Errors name the scenario and the field, and stop the run before setup.
  This covers an unknown executor, a field of another executor (such as
  `rate` on `constant-vus`), and an `exec` that is not exported.
- `scenarios` together with `vus`, `duration` or `stages` is an error.
- Unknown scenario keys produce a warning.

## Thresholds

**Thresholds are pass/fail criteria for the whole test**
([`examples/thresholds.ts`](../examples/thresholds.ts)). If any fails,
LoadTool prints the full summary, then exits with code **99**, as k6
does, so a CI job fails.

```typescript
export const options = {
  thresholds: {
    http_req_duration: ["p(95)<200", "p(99)<500"],  // milliseconds
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
    dropped_iterations: ["count==0"],
  },
};
```

An expression is `<aggregate> <op> <number>`, with `<`, `<=`, `>`, `>=`,
`==` or `!=`.

| Metric | Aggregates | Meaning |
|---|---|---|
| `http_req_duration` | `avg`, `min`, `max`, `med`, `p(N)` | Latency of every request sent, failed ones included, in ms |
| `http_req_failed` | `rate` | Failed requests ÷ requests (0–1) |
| `http_reqs` | `count`, `rate` | Requests; `rate` is per second |
| `checks` | `rate` | Passed checks ÷ checks run (0–1) |
| `iterations` | `count`, `rate` | Iterations that ran to their end |
| `dropped_iterations` | `count`, `rate` | Arrival-rate starts that found no free VU |
| `ws_*` | by kind: trend `avg`, `min`, `max`, `med`, `p(N)`; counter `count`, `rate`; rate `rate` | WebSocket metrics, in scripts that import `loadtool/ws` ([list](script-api.md#loadtoolws)) |
| `grpc_*` | by kind, as above | gRPC metrics, in scripts that import `loadtool/grpc` ([list](script-api.md#loadtoolgrpc)) |
| `graphql_*` | by kind, as above | GraphQL metrics, in scripts that import `loadtool/graphql` ([list](script-api.md#loadtoolgraphql)) |

**Rules:**

- **Checked before the test starts.** A typo, an unknown metric or an
  unsupported aggregate stops the run before setup, with exit code 1.
- **Evaluated once, at the end**, against the numbers the summary prints.
- **No data fails.** A threshold with no data, such as `checks` in a
  script that never calls `check`, fails and shows "no data".
- **Percentiles** are within ±0.78 %. A value that close to its limit is
  marked `≈`.
- **Exit code 1 wins.** If the run was interrupted or teardown failed, the
  exit code is 1, not 99.
- **Not supported yet:** `{ threshold, abortOnFail }`, thresholds on
  tagged sub-metrics such as `http_req_duration{status:200}`, and custom
  metrics. They are errors, not silently ignored.
