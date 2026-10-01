# ADR-004: Fixed-size latency histogram

- Status: Accepted
- Date: 2026-10-01
- Supersedes: the "all latency samples are kept exactly" part of
  [ADR-003](ADR-003-http-load-generator.md)

## Context

LoadTool kept every request latency (8 bytes each) in a slice per VU, then
copied and sorted them all at the end of the run.
- Memory grew with the number of requests: about 16–24 bytes per request
  at its peak (slice growth slack plus the merged copy).
- In a 120 s run at about 34k req/s, peak memory reached 162 MB against
  78 MB for a 20 s run.
- At the 13k req/s measured in the Phase 0 comparison, LoadTool would pass
  JMeter's 1,368 MB peak after roughly 60–95 minutes (calculated). The
  Phase 0 memory result therefore only held for short runs.
- Results are produced only at the end, so running out of memory late in a
  long test lost everything.

## Decision

1. **Record latencies in fixed-size, log-linear histograms** (the
   HdrHistogram layout), implemented in `internal/metrics/histogram.go`
   with no new dependency.
   - Values below 128 ns get one bucket each. Above that, each power of
     two is split into 64 sub-buckets, computed with bit operations.
   - Up to 1 hour, that is 2,368 buckets (about 19 KB per histogram).
     Larger values share the last bucket.
2. **Precision:** a percentile is the midpoint of the bucket holding its
   nearest rank, so it is within **±0.78 %** of the exact value.
   - It is clamped to the exact min and max, so a single sample is exact.
   - Counts, min, max and mean remain exact.
3. **16 shared shards.** Each shard holds a histogram for successful and
   one for failed requests, with atomic counters.
   - VU *i* records into shard *i* mod 16.
   - Total memory is about 0.6 MB, independent of VU count, duration and
     request rate.
   - Per-VU counters that change rarely (unsent requests, script errors)
     stay in each VU's `Recorder` without atomics.
4. **Merge** sums the shards' buckets: about 0.3 ms instead of a sort over
   every sample.

## Alternatives considered

- **One histogram per VU** (no shared state on the hot path): about 30 MB
  fixed at 1,000 VUs. Measured parallel recording into shared shards costs
  about 3 ns per record across 12 CPUs, so sharing does not create a
  bottleneck and saves most of that memory.
- **The `hdrhistogram-go` library:** same idea, but adds a dependency and
  is not built for concurrent atomic updates.
- **Reservoir sampling:** fixed memory, but percentile error depends on
  luck rather than being bounded.
- **Keeping exact samples:** rejected because memory grows with test
  length.

## Consequences

- **Memory:** recording allocates 0 B per request (`BenchmarkRecorderRecord`).
  Peak memory measured flat across run length: 62.0 MB at 20 s and 61.8 MB
  at 120 s, with 50 VUs. See
  `benchmarks/results/2026-10-01-latency-histogram.md`.
- **Precision:** reported percentiles are approximate within ±0.78 %. k6
  and JMeter report exact percentiles, but their results also carry
  measurement noise much larger than 0.78 % (see the Phase 0 reports).
- **Speed:** recording costs about 16–24 ns instead of 6–12 ns, because of
  the atomics. That is negligible next to an HTTP request.
- **Range:** latencies above 1 hour are not resolved in percentiles, but
  are still exact in `Max`.
