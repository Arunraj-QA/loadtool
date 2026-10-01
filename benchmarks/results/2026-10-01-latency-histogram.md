# 2026-10-01 — Latency histogram (ADR-004): memory and cost

This compares LoadTool before (commit `eea4dab`, exact latency samples) and
after the change to fixed-size shared histograms
([ADR-004](../../docs/decisions/ADR-004-latency-histogram.md)).

- **Environment:** i5-1235U (12 logical CPUs), 15.7 GB RAM, Windows 11 Pro
  10.0.26200, go1.27.0, on AC power. Laptop not isolated.
- This is not a k6 or JMeter comparison.

## Measured: peak memory against run length

**Setup:**
- `benchmarks/server` on `127.0.0.1:8080` with `-delay 0`, which maximises
  the request rate.
- `loadtool run benchmarks/loadtool/scenario.ts --vus 50`.
- Peak private bytes sampled every 250 ms.
- One run each, old and new alternating.

| Duration | Build | Peak private bytes | Requests |
|---|---|---|---|
| 20 s | old (exact samples) | 77.9 MB | 945,324 |
| 20 s | new (histogram) | 62.0 MB | 878,419 |
| 120 s | old (exact samples) | 162.1 MB | 4,113,642 |
| 120 s | new (histogram) | 61.8 MB | 3,712,588 |

**Calculated:**
- Old build: (162.1 − 77.9) MB ÷ (4,113,642 − 945,324) requests ≈
  **27.9 bytes per request**.
- New build: no growth (−0.2 MB).

These are single runs, so they show the trend clearly but don't give
variance.

## Measured: micro-benchmarks

```bash
go test -run '^$' -bench 'Record|Merge' -benchmem -count=3 ./internal/metrics/
```

| Benchmark | Before (exact samples) | After (histogram) |
|---|---|---|
| `BenchmarkRecorderRecord` | 6.4–12.5 ns/op, 41–46 B/op, 0 allocs | 15.9–24.3 ns/op, **0 B/op**, 0 allocs |
| `BenchmarkRecorderRecordParallel` (12 CPUs, shared shards) | — | 3.0 ns/op |
| `BenchmarkMerge` (1,000 recorders × 1,000 samples) | 67.4–67.8 ms/op, 8.0 MB/op, 1 alloc | **0.30–0.34 ms/op, 39.7 KB/op**, 12 allocs |

The "before" B/op is the amortized cost of the growing sample slice.

## Precision

Verified by tests, not by these benchmarks:
- `TestPercentilesMatchExact`: 200,000 log-uniform samples from 10 µs to
  10 s. Every percentile is within ±0.78 % of the exact nearest-rank value.
- `TestBucketBounds`: every value up to 4,096 ns, plus 200,000 random
  values up to 1 h, lands in a bucket whose midpoint is within ±0.78 %.
