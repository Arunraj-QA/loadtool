# ADR-012: HTML report and time series

- Status: Accepted
- Date: 2026-10-06

## Context

Phase 1 roadmap item 10 is a self-contained HTML report.

- **What a summary cannot show.** A report is most useful when it shows
  how the test behaved over time: latency creeping up, errors starting
  at some point, a ramp. The console summary and the JSON summary
  (ADR-011) only hold end-of-test totals.
- **Constraints:**
  - Per-VU memory and the request path must not get heavier: Phase 0 exit
    criterion, ADR-004.
  - The report must work offline and as a CI attachment.

## Decision

1. **Per-second series sampled from the existing histograms.**
   - Once a second, a sampler snapshots the run's shared histogram shards
     (ADR-004) and subtracts the previous snapshot.
   - That gives, for the requests completed in that second: count, failed
     count, mean and p50/p95/p99, within the histograms' ±0.78 %.
   - The request path is unchanged.
   - The sampler keeps one fixed snapshot buffer, about 38 KB, and stores
     about 80 B per second, so an hour is about 0.3 MB.
   - A snapshot takes about 70 µs with no allocations, measured with
     `BenchmarkSample` over 16 shards.
   - A snapshot is not atomic across buckets, so a request recorded during
     one may count in the next second; totals are unaffected (tested).
   - Requests that were never sent, such as invalid URLs, have no latency
     and are not in the series.
2. **VUs over time** is the number of *active* VUs (k6's `vus`), computed
   from the scenario definitions at each sample rather than counted:
   - **constant-vus:** its VUs while it runs.
   - **ramping-vus:** the stage line's level, in the same integer arithmetic
     as the scheduling (a property test checks they agree).
   - **constant-arrival-rate:** its VU pool while it runs; whether the pool
     kept up shows in dropped iterations.

   A first version counted VUs in an iteration with one shared atomic
   counter. A worst-case benchmark (no-op iterations, 64 VUs) showed it
   raised per-iteration overhead from about 2 ns to about 52 ns through
   contention, so it was replaced; iterations now touch no shared state
   for the series.
3. **The sampler runs in a goroutine of its own.** It starts with the test
   clock, is stopped once every VU and scheduler has returned, and is
   waited for. It adds a final point for the last, partial second.
4. **`--report-html <file>`** writes one HTML file:
   - Inline CSS, with light and dark themes through
     `prefers-color-scheme`.
   - The console summary's content, as tables with ✓/✗.
   - Three SVG charts rendered in Go: latency p50/p95/p99, requests and
     failures per second, and active VUs. A second without
     requests is a gap in the latency lines, not a zero.
   - No JavaScript, no external fonts, styles or images: it opens offline,
     anywhere.
   - It is rendered with `html/template`, so script text such as check
     names and errors is escaped.
   - Charts appear when there are at least two points (runs of about two
     seconds or more).
5. **The JSON summary gains `series`** under schema version 1. This is
   additive, so the version is unchanged (ADR-011).
6. **Both files are written like the JSON summary:** atomically, whenever
   there is a result. On Windows the final rename is retried for up to
   about a second, because virus scanners, indexers and browsers briefly
   hold a freshly written file open, and replacing an open file fails
   there. This was seen while testing.

## Consequences

- **CI artifact.** A test run can attach `report.html`. Reviewers see the
  run's shape without the tool.
- **No iteration cost.** The same worst-case benchmark measures about 2 ns
  per iteration before and after; see
  `benchmarks/results/2026-10-06-time-series/`.
- **Resolution.** The series has one-second resolution and covers
  requests by completion time. Finer resolution or per-scenario series
  are not part of Phase 1.
