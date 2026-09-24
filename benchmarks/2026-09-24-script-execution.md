# 2026-09-24 — Script execution overhead

Environment is the same as
[2026-09-24-microbenchmarks.md](2026-09-24-microbenchmarks.md): i5-1235U,
15.7 GB, Windows 11, go1.27.0. The laptop was not isolated.

These measurements are **not** a k6 or JMeter comparison and do not show
the Phase 0 exit criterion.

## Microbenchmarks

```bash
go test -run='^$' -bench=. -benchmem -count=3 ./internal/script/ ./internal/httpclient/
```

| Benchmark | ns/op (3 runs) | B/op | allocs/op |
|---|---|---|---|
| `script.BenchmarkNewVU` | 68,892 / 53,450 / 51,685 | 39,610 | 458 |
| `script.BenchmarkIterateEmpty` | 368.7 / 382.0 / 370.9 | 208 | 4 |
| `script.BenchmarkIterateHTTPGet` | 151,213 / 137,164 / 124,305 | ~7,710 | 90 |
| `httpclient.BenchmarkDo` (no script) | 151,820 / 151,982 / 114,181 | ~6,210 | 73 |

- A script-driven GET allocates about 1.5 KB and 17 more objects than a
  direct `httpclient.Do`. Both include the in-process test server's
  allocations.
- The ns/op spread is larger than the difference between the two, so no
  claim is made about script latency overhead from these numbers.
- `BenchmarkNewVU` reports bytes **allocated** while creating a VU, not
  bytes retained.

## End-to-end A/B: before and after script execution

- **Old:** commit `4e1e539`, fixed GET via `--url`.
- **New:** script `http.get("http://127.0.0.1:8080/")`.
- **Target:** a local Go server with 10 ms of handler latency, on the same
  laptop. Duration 5 s. The two binaries alternated, 3 runs each.

| VUs | Build | req/s (runs 1 / 2 / 3) | errors | p50 |
|---|---|---|---|---|
| 100 | old | 9,096 / 9,008 / 9,151 | 0% | 10.70 / 10.75 / 10.68 ms |
| 100 | new | 9,218 / 9,090 / 9,180 | 0% | 10.61 / 10.70 / 10.65 ms |
| 1000 | old | 32,278 / 7,775 / 8,292 | 2.5% / 25.1% / 46.3% | 22.7 / 60.1 / 73.7 ms |
| 1000 | new | 5,425 / 7,713 / 7,587 | 8.4% / 12.9% / 10.8% | 105.9 / 94.7 / 92.9 ms |

### Findings

- At 100 VUs the builds are the same within noise. Throughput is at the
  server's limit (100 VUs / ~10.7 ms ≈ 9.3k req/s).
- At 1,000 VUs **both** builds vary widely between runs.
  - The errors are `connectex: ... actively refused`: the target's accept
    backlog overflows when 1,000 VUs connect at the same instant.
  - A CPU profile of a 1,000-VU run showed about 51% of samples in
    `Transport.dialConn` and 44% in `Closesocket`, so connections were
    being re-dialed instead of reused.
  - On Windows, Go's profiler also samples threads blocked in system
    calls, so these percentages overstate real CPU use.
- **Conclusion:** this local Windows setup cannot produce valid 1,000-VU
  measurements. The Phase 0 exit benchmark needs a target that accepts
  1,000 concurrent connections, for example a larger listen backlog or
  another machine, plus a warm-up or ramp so connection setup does not
  dominate.
