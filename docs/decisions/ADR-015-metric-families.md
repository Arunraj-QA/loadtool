# ADR-015: Metric families for protocols

- Status: Accepted (2026-10-08, Phase 2)
- Date: 2026-10-08

## Context

`metrics.Recorder.Record(latency, ok)` records into one ok/failed
histogram pair, which becomes `http_req_duration`, `http_req_failed`
and `http_reqs`. The metric has no protocol dimension: a WebSocket or
Kafka operation recorded through it would be counted as an HTTP request.

HTTP-specific names are also built into:

- `metrics.Summary` (`Requests`, `P95`, …);
- the threshold table (`internal/thresholds`);
- the JSON summary and its schema (closed objects, ADR-011);
- the console and HTML reports.

Custom metrics are not supported (ADR-008), so there is no metric
registry to extend.

## Decision

### 1. Metric definitions

Each protocol module declares metric definitions (ADR-014):

```go
type Def struct {
    Name string // "grpc_req_duration"
    Kind Kind   // Trend, Counter or Rate
}
```

| Kind | Holds | Threshold aggregates |
|---|---|---|
| Trend | An ok/failed pair of the existing sharded histograms | `avg`, `min`, `max`, `med`, `p(N)` |
| Counter | An int64 per recorder | `count`, `rate` (per second) |
| Rate | True samples and total. For a `*_failed` family, true means failed, so its rate is the error rate, as with `http_req_failed` | `rate` |

### 2. Recording

- **Families get small integer IDs**, fixed before the run from the
  imported modules.
- **Recording is an array index plus an atomic histogram update,** with
  no map lookup and no allocation:
  - `rec.Trend(id, d, ok)`;
  - `rec.Add(id, n)`;
  - `rec.Rate(id, v)`.
- **Shards are allocated only for families of imported modules.** That is
  about 0.6 MB per Trend per run, independent of the VU count.

### 3. HTTP stays as it is

- `Record`, `RecordUnsent`, `RecordProtocol` and the `Summary` HTTP
  fields are unchanged.
- `http_*` thresholds keep their entries.
- HTTP-only output stays byte-identical, and the golden files must not
  change.

### 4. Merging and thresholds

- **`Merge` folds the families** into `Summary.Families`, as it already
  does with histograms and checks.
- **Thresholds look families up** by name and kind. They are still
  validated before setup: an unknown metric lists every registered name.
- **`checks` and `iterations` remain global**, across protocols.

### 5. Names

`<protocol>_<what>`, following the existing `http_*` style. Each protocol
ADR lists its names:

| Protocol | Planned names |
|---|---|
| WebSocket | `ws_connecting`, `ws_session_duration`, `ws_sessions`, `ws_msgs_sent`, `ws_msgs_received`, `ws_session_failed` |
| gRPC | `grpc_req_duration`, `grpc_reqs`, `grpc_req_failed` |
| GraphQL | `graphql_req_duration`, `graphql_reqs`, `graphql_req_failed` |
| Kafka | `kafka_produce_duration`, `kafka_messages_produced`, `kafka_produce_failed`, `kafka_consume_duration`, `kafka_messages_consumed` |

**GraphQL** calls made with `loadtool/graphql` are counted only under
`graphql_*`, never `http_*` (Phase 2 scope decision 1).

### 6. Reports show a family only when it has data

- **Console:** one section per protocol.
- **JSON:** `metrics.<name>`, with Trend values in milliseconds like
  `http_req_duration`. This is additive, so the schema stays at version 1
  (ADR-011), and `docs/schemas/summary-v1.schema.json` gains the
  definitions in the same change.
- **HTML:** a table per protocol, plus summary cards.
- **Time series:** the per-second series stays HTTP-only in Phase 2.

## Consequences

- **Protocols never distort HTTP numbers.** A test enforces that
  non-HTTP recording leaves `http_*` untouched.
- **The families are a base a later custom-metrics feature could
  reuse;** that feature is not part of Phase 2.
- **Schema change.** Anyone validating against an old copy of the closed
  schema rejects summaries that contain new families. The schema file is
  updated in the same change, and the JSON summary docs say so.

## Alternatives considered

- **A protocol label on the existing histograms:** rejected. It changes
  the HTTP hot path, and every HTTP metric would need a filter.
- **A string-keyed map per record call:** rejected. A map lookup on
  every operation is a hot-path allocation and a source of lock
  contention.
- **Counting GraphQL as HTTP as well:** rejected. An HTTP 200 with
  GraphQL `errors` would be a success under one metric and a failure
  under another.

## Implementation notes (2026-10-08, Phase 2 step 1)

These details were settled while building the families. They do not
change the decision.

- **Storage is run-wide.** One `metrics.Families` holds a run's
  families, and each VU's recorder is pointed at it with `UseFamilies`.
  - **The engine does not change.** It still creates recorders as
    before.
  - **Setup and teardown are left out for free.** Their throwaway
    recorder has no families, so whatever they record is dropped.
- **Recording uses the same shards as HTTP.**
  - Trend shards reuse the HTTP histogram type.
  - Counters and rates are atomics padded to a cache line, one per
    shard.
  - Recording allocates nothing (`TestFamilyRecordingDoesNotAllocate`).
- **Rate counts true samples.** A Rate's value is the fraction of true
  samples, so a `*_failed` family records true for a failure, with the
  meaning `http_req_failed` has. The summary field is `Trues`, and the
  JSON field is `trues`.
- **Names:** `metrics.NewFamilies` checks the `<protocol>_<what>` form
  and rejects the built-in names.
- **JSON.**
  - Each family object carries a `kind`.
  - The schema accepts the prefixes `ws_`, `grpc_`, `graphql_` and
    `kafka_`. A new protocol adds its prefix to the schema in the same
    change.
  - A generic pattern would also match the built-in `http_*` keys, and
    JSON Schema applies both `properties` and `patternProperties`.
- **Console.** Families are printed after the HTTP latency, as
  `p95=3.00ms`. `benchmarks/measure.ps1` reads the first `p95 <value>`,
  so it can never pick up a family's percentile.
- **Unchanged output.** Console, JSON and HTML output of a run without
  families is byte-identical to before; the existing golden files did
  not change.
