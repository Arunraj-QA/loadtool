# 2026-10-06 — Phase 1 step 11 (time series): cost of sampling

Step 11 adds a per-second time series for the HTML report (ADR-012). It
must not add cost to the request or iteration path. This record holds
the measurements behind that design.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 16 GB, Windows 11 Enterprise 10.0.26300 |
| Power | **On battery (18 %)**: CPU throttled. Absolute numbers are not comparable with earlier records; each comparison below ran both variants under the same conditions, interleaved. |
| Go | go1.27.0 windows/amd64 |
| Before | commit `9ed9814` (`phase-1-mvp` after step 10) |

## 1. Sampler cost

`BenchmarkSample` (`internal/metrics`) takes one snapshot over 16 shards,
as in a 1,000-VU run:

```
BenchmarkSample-12    17331    67407 ns/op    0 B/op    0 allocs/op
```

That is about 67 µs per second of test with no allocations: about
0.007 % of one core. Memory is fixed: two snapshot buffers of about 19 KB
each, plus about 80 B per stored point.

## 2. Iteration overhead (worst case)

**Benchmark.** A temporary benchmark (not committed) ran `engine.Run`
with 64 VUs and no-op iterations for 300 ms, reporting wall time per
completed iteration. No-op iterations maximize any per-iteration cost.

**Variants.** It ran on `9ed9814` and on two versions of step 11, three
alternating rounds each:

| Variant | ns per iteration |
|---|---|
| Before (`9ed9814`) | 2.37, 2.14, 1.94 |
| Step 11, first version: a shared atomic "VUs in an iteration" counter | 52.9, 52.2 (and one outlier at 26,011) |
| Before (second session) | 2.04, 1.91, 1.97 |
| Step 11, final: active VUs computed from the scenario definitions | 2.03, 2.00, 1.92 |

**Results:**

- The shared counter made every iteration contend on one cache line,
  about 25 times the overhead in this worst case. That is negligible next
  to an HTTP iteration (about 100 µs), but about 10 % for a fast CPU-bound
  script iteration (about 0.5 µs). It was replaced.
- The final version measures the same as before: the time series adds
  nothing to the iteration path.

## 3. Process level (not repeated for the final version)

**What ran.** A 1,000-VU A/B (`../2026-10-04-dsl-core-memory/ab.ps1`,
3 alternating rounds of 20 s) was run against the *first* version, which
had the shared counter.

**Results** (both binaries ran at about half their usual throughput,
because the laptop was on battery):

| Variant | Peak private MB | req/s |
|---|---|---|
| Before | 190.1–200.3 | 17,314–19,922 |
| First version | 189.2–203.2 | 15,356–19,449 |

- The ranges overlap: an overhead of tens of nanoseconds per iteration is
  invisible next to 10 ms requests.
- The final version changes the iteration path less than that first
  version (by section 2 it does not change it at all), so the process-level
  run was not repeated.
