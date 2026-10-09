# JSON summary

`loadtool run --out json` writes the end-of-test summary as JSON, for CI
jobs and other tools. It holds the same numbers as the console summary:
both are rendered from the same result.

```bash
loadtool run test.ts --out json | jq '.outcome'          # JSON on stdout
loadtool run test.ts --out json=summary.json            # JSON in a file
jq '.metrics.http_req_duration.p95' summary.json
```

**The forms:**

- **`--out json`** writes the JSON to stdout. The console summary then
  goes to stderr, so stdout is valid JSON.
- **`--out json=<file>`** writes a file and keeps the console summary on
  stdout. `--summary-json <file>` is the same.
- **`--out` is repeatable.**

**A machine-readable schema:**
[`schemas/summary-v1.schema.json`](schemas/summary-v1.schema.json) (JSON
Schema 2020-12).

- Every object is closed (no other fields) and every field is always
  present, except the executor-specific fields of a scenario.
- Tests validate LoadTool's output against it.

**When it is written:**

- **Always when the test produced a result:** completed, interrupted,
  failed thresholds (exit 99) or failed teardown.
- **Never when the test could not start** (script errors, invalid options,
  setup failure). Check the exit code first.
- **Atomically.** The file is written to a temporary file in the same
  directory and renamed, so it is never half-written. If it cannot be
  written, the run exits with code 1 after printing the console summary.

## Versioning

`schemaVersion` is `1`.

- New fields may be added without changing the version, so consumers
  should ignore fields they do not know.
- Removing or renaming a field, or changing its meaning or unit, increases
  the version ([ADR-011](decisions/ADR-011-json-summary.md)).

## Determinism

The same result always gives the same document:

- keys appear in a fixed order;
- arrays keep a defined order (checks in first-run order, thresholds by
  metric and then as written, scenarios by name, series by time);
- absent values are `null`, not omitted (protocol metric families are the
  exception: an unused family is left out).

Only measured values (timings, counts) and `startedAt` differ between
runs.

## Units

- **Durations** are milliseconds, as numbers (fields end in `Ms`, plus the
  `http_req_duration` values).
- **Fractions** (`rate` of `http_req_failed`, `checks`) are between 0 and
  1. They are `null` when there was nothing to count.
- **Rates per second** (`rate` of `http_reqs`, `iterations`,
  `dropped_iterations`) are over `elapsedMs`.

## Fields

| Field | Type | Meaning |
|---|---|---|
| `schemaVersion` | number | `1` |
| `tool.name`, `tool.version` | string | `"loadtool"` and its version (`"dev"` for local builds) |
| `script` | string | Script path as given on the command line |
| `status` | string | `"completed"` or `"interrupted"` (Ctrl+C; metrics are partial) |
| `outcome.passed` | boolean | `true` when the run passed: not interrupted, teardown succeeded, every threshold passed |
| `outcome.exitCode` | number | The exit code `loadtool run` returns for this result: `0` passed, `99` thresholds failed, `1` interrupted or teardown failed (which win over `99`) |
| `outcome.reasons` | string[] | Why it did not pass, such as `"threshold failed: http_req_failed rate<0.01"`; empty when it passed |
| `startedAt` | string | When the test clock started, UTC, RFC 3339 with milliseconds |
| `elapsedMs` | number | From the clock start until the last VU stopped |
| `teardownError` | string | The teardown failure, `""` if none |
| `config.vus` | number | VUs across all scenarios |
| `config.durationMs` | number | When the last scenario stops starting iterations |
| `config.scenarios[]` | object | One per scenario, see below |
| `metrics.http_reqs` | `{count, rate}` | Requests |
| `metrics.http_req_failed` | `{rate, failed, count}` | Failed requests: transport errors and status ≥ 400 |
| `metrics.http_req_duration` | `{count, min, avg, max, p50, p90, p95, p99}` or `null` | Latency of every request sent, failed ones included; `null` if none was sent |
| `metrics.http_req_duration_successful` | `{count, p50, p90, p95, p99}` or `null` | Latency of successful requests only |
| `metrics.iterations` | `{count, rate}` | Iterations that ran to their end |
| `metrics.dropped_iterations` | `{count, rate}` | Arrival-rate starts with no free VU |
| `metrics.checks` | `{rate, passes, fails, count}` | All checks together |
| `metrics.script_errors` | `{count, first}` | Iterations that threw, and the first message |
| `metrics.http_protocols` | `{http1, http2, other}` | Responses by HTTP version (added later in Phase 1, additive) |
| `checks[]` | `{name, passes, fails, rate, firstError}` | Each check, in the order first run |
| `thresholds[]` | `{metric, expression, passed, noData, observed, unit, approximate}` | Each threshold; `observed` is `null` with no data; `unit` is `ms`, `fraction`, `per-second` or `count` |
| `series[]` | `{atMs, vus, http_reqs, http_req_failed, http_req_duration}` | The time series, one point per second plus a final, partial one ([ADR-012](decisions/ADR-012-html-report-and-time-series.md)). Counts are of requests that completed in the interval ending at `atMs`; `vus` is the active VUs at `atMs` (from the scenario definitions); `http_req_duration` is `{avg, p50, p95, p99}`, or `null` for an interval without requests. Added in Phase 1 step 11 (additive). |

**Protocol metric families** (Phase 2,
[ADR-015](decisions/ADR-015-metric-families.md)) are additional keys in
`metrics`, written by protocol modules: `ws_*`
([WebSocket](script-api.md#loadtoolws)), `grpc_*`
([gRPC](script-api.md#loadtoolgrpc)), `graphql_*`
([GraphQL](script-api.md#loadtoolgraphql)) and `kafka_*`
([Kafka](script-api.md#loadtoolkafka)).

- **Where they appear:** after the keys above, in a fixed order, and
  only when they recorded something. A run without them writes exactly
  the document described above.
- **Names:** `<protocol>_<what>`, with the prefixes `ws_`, `grpc_`,
  `graphql_` and `kafka_`.
- **Shapes:** each carries a `kind` that gives its shape:

| `kind` | Fields |
|---|---|
| `trend` | `count`, `failed`, `min`, `avg`, `max`, `p50`, `p90`, `p95`, `p99` (milliseconds; every sample, failed ones included) |
| `counter` | `count`, `rate` (per second) |
| `rate` | `rate`, `trues`, `count`: the fraction of true samples. For a `*_failed` family, true means failed, so `rate` is the error rate, like `http_req_failed` |

Thresholds can name a family (`thresholds[].metric`) of a module the
script imports.

**Precision.** Latency percentiles are within ±0.78 %, while `min`, `avg`
and `max` are exact (ADR-004). `approximate: true` marks a percentile
threshold whose observed value is within that error of its limit.

### Scenarios

Every scenario has `name`, `executor`, `exec`, `startTimeMs` and
`gracefulStopMs`, plus the fields of its executor:

| Executor | Fields |
|---|---|
| `constant-vus` | `vus`, `durationMs` |
| `ramping-vus` | `startVUs`, `stages[] {durationMs, target}`, `gracefulRampDownMs` |
| `constant-arrival-rate` | `rate`, `timeUnitMs`, `durationMs`, `preAllocatedVUs` |

A run with `--vus`/`--duration` (or `options.vus`/`duration`) has one
`constant-vus` scenario named `default`.

## Example

```json
{
  "schemaVersion": 1,
  "tool": { "name": "loadtool", "version": "v0.1.0" },
  "script": "examples/thresholds.ts",
  "status": "completed",
  "startedAt": "2026-10-06T04:00:00.123Z",
  "elapsedMs": 10003.2,
  "teardownError": "",
  "config": {
    "vus": 10,
    "durationMs": 10000,
    "scenarios": [
      { "name": "default", "executor": "constant-vus", "exec": "default",
        "startTimeMs": 0, "gracefulStopMs": 30000, "vus": 10, "durationMs": 10000 }
    ]
  },
  "metrics": {
    "http_reqs": { "count": 9120, "rate": 911.7 },
    "http_req_failed": { "rate": 0, "failed": 0, "count": 9120 },
    "http_req_duration": { "count": 9120, "min": 10.02, "avg": 10.9, "max": 15.1,
      "p50": 10.81, "p90": 11.47, "p95": 11.6, "p99": 12.12 },
    "http_req_duration_successful": { "count": 9120,
      "p50": 10.81, "p90": 11.47, "p95": 11.6, "p99": 12.12 },
    "iterations": { "count": 9120, "rate": 911.7 },
    "dropped_iterations": { "count": 0, "rate": 0 },
    "checks": { "rate": 1, "passes": 9120, "fails": 0, "count": 9120 },
    "script_errors": { "count": 0, "first": "" }
  },
  "checks": [
    { "name": "status is 200", "passes": 9120, "fails": 0, "rate": 1, "firstError": "" }
  ],
  "thresholds": [
    { "metric": "http_req_duration", "expression": "p(95)<200", "passed": true,
      "noData": false, "observed": 11.6, "unit": "ms", "approximate": false }
  ]
}
```
