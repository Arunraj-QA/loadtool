# 2026-10-08 — Metric families: HTTP before and after

Phase 2 step 1 adds protocol metric families (ADR-015). This run checks
that the change costs the HTTP path nothing. HTTP's recording code did
not change, but `metrics.Recorder` gained two fields, and
`NewRecorders` sets one more.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`PowerOnline: True`); no other work was started during the runs |
| Go | go1.27.0 windows/amd64 |
| Builds | **base:** `143d0a6` (`phase-2-protocol-breadth` before the change); **new:** the `p2/metric-families` working tree |

## Method

**Two packages:** test binaries were built for `internal/metrics` and
`internal/script` from both trees.

**Six rounds,** alternating base and new within each round (`raw/`):

- `internal/metrics`: `BenchmarkRecorderRecord`,
  `BenchmarkRecorderRecordParallel`, `BenchmarkMerge`,
  `BenchmarkRecordCheck`;
- `internal/script`: `BenchmarkIterateEmpty`, `BenchmarkIterateHTTPGet`,
  `BenchmarkNewVU`, `BenchmarkVURetainedMemory`.

Each benchmark ran with `-benchmem` and the default benchtime.

## Measured

Medians of six runs, with ranges:

| Benchmark | Base | New | Change |
|---|---|---|---|
| `RecorderRecord` (HTTP sample) | 15.9 ns (15.6–19.0) | 16.4 ns (15.9–21.6) | within range |
| `RecorderRecordParallel` | 2.7 ns (2.6–2.8) | 2.7 ns (2.4–4.0) | within range |
| `RecordCheck` | 7.8 ns (7.7–8.9) | 8.2 ns (7.7–10.8) | within range |
| `Merge` | 311 µs (252–387) | 279 µs (112–312) | within range |
| `IterateEmpty` | 797 ns (369–980) | 846 ns (457–1,029) | within range |
| `IterateHTTPGet` | 214 µs (73–242) | 209 µs (95–274) | within range |
| `NewVU` | 10.9 µs (4.9–14.8) | 12.1 µs (7.4–15.1) | within range |

**Memory was identical** in every benchmark:

- **Bytes and allocations per operation are equal.** Examples: 0 for
  recording, 81 allocations for an HTTP GET iteration, 59 for creating a
  VU.
- **Retained memory per VU is equal byte for byte**, from 4,720 B (no
  imports) to 6,024 B (HTTP plus `sleep`, after the first iteration).

`BenchmarkVURetainedMemory` reports ns/op as well, but that is the
time to create 1,000 VUs, and its range is wide in both builds (for
example 11.3–14.4 ms against 11.7–34.4 ms). It is not a hot-path
measure.

**The new family benchmarks** (64 VUs, new build only):

- `BenchmarkFamilyTrendParallel` and `BenchmarkFamilyCounterParallel`
  record with 0 allocations;
- `TestFamilyRecordingDoesNotAllocate` asserts this.

## Conclusion

- **No measurable regression in the HTTP path.** Every time-per-operation
  difference lies within the overlapping run-to-run ranges, and goes
  both ways.
- **Memory per operation and per VU is unchanged.**

This supports Phase 2 exit criterion 3 (no per-VU cost for unused
modules) for this step. The 1,000-VU process benchmark for exit
criterion 2 runs at the end of Phase 2.

**Caveats:**

- One laptop.
- Nanosecond-scale timings on Windows are noisy, which is why base and
  new were alternated and medians are reported.
