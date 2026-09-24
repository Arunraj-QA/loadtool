# 2026-09-24 — Metrics and request-path microbenchmarks

These are Go microbenchmarks of LoadTool internals. They are **not** a
comparison with k6 or JMeter and do not show the Phase 0 exit criterion.

## Environment

| Item | Value |
|---|---|
| CPU | 12th Gen Intel Core i5-1235U (10 cores / 12 threads) |
| RAM | 15.7 GB |
| OS | Windows 11 Enterprise 10.0.26200 |
| Go | go1.27.0 windows/amd64 |
| Power / background load | Laptop, not isolated; normal desktop apps running |

## Command

```bash
go test -run='^$' -bench=. -benchmem -count=3 ./internal/...
```

## Results

| Benchmark | ns/op (3 runs) | B/op | allocs/op |
|---|---|---|---|
| `metrics.BenchmarkRecorderRecord` | 14.29, 11.71, 11.46 | 45–49 | 0 |
| `metrics.BenchmarkMerge` (1,000 recorders × 1,000 samples) | 71.6 ms, 69.0 ms, 68.2 ms | 8.0 MB | 1 |
| `httpclient.BenchmarkGet` (local `httptest` server) | 86,720, 79,627, 80,578 | ~6,214 | 73 |

## Notes

- `Record` makes no allocations per call. The B/op value is the amortized
  cost of the latency slice growing (8 bytes per sample plus doubling
  headroom).
- `Merge` makes one allocation (the combined sample slice) and its cost is
  mostly the sort. That happens once, after the run.
- `BenchmarkGet` runs the server in the same process, so its B/op and
  allocs/op **include server-side allocations**. They are an upper bound
  for the client path, not a client-only figure.
- On this machine Go's monotonic clock advanced in steps of about 0.34 ms
  (measured with a busy-loop over `time.Since`). Request latencies below
  that can read as 0, and every latency is rounded to that step.
