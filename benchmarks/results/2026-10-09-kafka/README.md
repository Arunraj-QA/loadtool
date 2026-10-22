# 2026-10-09 — Kafka produce at 10 to 1,000 VUs

The first measurement of the Kafka module (ADR-022): each VU produces
with its own producer at 10, 100, 500 and 1,000 VUs. It is a baseline
for this module and records what one producer per VU costs. It is not a
measurement of Kafka, nor a comparison with other tools.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`PowerOnline: True`); no other work was started during the runs. The corporate security agents were not idle: total CPU was 18–31 % before the runs and 42–94 % just after (`background-cpu.txt`) |
| Target | The demo API's **in-process broker** (franz-go's `kfake`, not a real Kafka): `bin/demo-api.exe -kafka-port 9092`, on the **same machine**, a **fresh process for every run** (kfake keeps every message in memory); topic `orders`, 3 partitions |
| LoadTool | branch `p2/kafka` at `64cad74` |
| Go | go1.27.0 windows/amd64 |

## Method

```powershell
./benchmarks/kafka-produce.ps1 -OutDir <dir>   # VUs 10, 100, 500, 1000; 30 s runs; 3 rounds; 10 s warm-ups
```

**The scenario** (`benchmarks/loadtool/kafka-produce.ts`):

- Each VU has its own `kafka.Producer`, so there is one franz-go client
  per VU (scope decision 4).
- Each iteration produces one 100-byte message (64 distinct keys) and
  waits for its acknowledgement: no linger and no think time. The test
  is closed-loop, so throughput is what LoadTool and the broker together
  sustain.

**The harness:**

1. One warm-up per level, then three rounds with the levels in turn.
2. Messages, rates, latency and failures are read from LoadTool's JSON
   summary (`kafka_*` metrics).
3. Peak private memory, working set and CPU (as a percentage of the
   whole machine) are sampled from the LoadTool process every 200 ms.
4. The broker process's peak private memory and CPU are sampled the same
   way.

Every run is in `raw/runs.jsonl`, with each run's log and JSON summary.

## The measurements

Medians of three 30 s runs, with ranges:

| VUs | Messages/s | p50 ms | p95 ms | p99 ms | Failed | LoadTool peak private MB | LoadTool CPU % | Broker peak private MB |
|---|---|---|---|---|---|---|---|---|
| 10 | 24,969 (24,094–25,783) | 0.54 (0.54–0.54) | 0.70 (0.70–0.72) | 1.19 (1.19–1.24) | 0 | 132.9 (128.2–134.1) | 42.3 (41.9–42.5) | 341.5 (341.3–342.1) |
| 100 | 33,919 (33,412–34,602) | 2.61 (2.57–2.64) | 6.13 (6.00–6.13) | 9.76 (9.37–9.76) | 0 | 193.5 (191.4–324.2) | 51.1 (49.0–51.1) | 457.2 (456.7–483.2) |
| 500 | 33,199 (32,210–33,521) | 13.04 (12.91–13.17) | 25.30 (25.03–29.23) | 40.11 (39.58–51.12) | 0 | 453.2 (445.0–512.8) | 50.5 (47.4–51.4) | 516.4 (505.1–531.5) |
| 1,000 | 29,952 (24,836–31,578) | 25.82 (25.03–26.87) | 52.17 (46.92–70.78) | 89.65 (77.07–143.65) | 0 | 904.3 (864.9–930.4) | 47.6 (39.7–50.4) | 584.5 (507.1–605.2) |

**There were no failed produces and no script errors** in any run: 722,832
to 1,038,108 acknowledged messages per 30 s run.

### What the numbers show

- **Memory grows by about 0.8 MB per VU.** From 10 to 1,000 VUs, peak
  private memory rose from 133 to 904 MB. That is (904 − 133) / 990 ≈
  0.78 MB per additional VU, almost all of it the VU's franz-go client.
  For comparison, the HTTP exit scenario used 183–196 MB at 1,000 VUs
  (2026-10-07) and gRPC 352 MB (2026-10-09). These are different
  scenarios, so the comparison only shows scale.
- **Throughput levels off at about 33,000 messages/s from 100 VUs.**
  Beyond that, latency grows with the VU count (p50 2.6 → 13 → 26 ms),
  as in a closed loop at capacity. LoadTool and the in-process broker
  share the machine: together they used about 75 % of it, against 18–31 %
  of background load.
- **The first produces at 500 and 1,000 VUs are slow.** The maximum
  produce latency was 5.4–17.1 s in every run at 500 and 1,000 VUs
  (17–80 ms at 10 and 100 VUs), while p99 stayed at 40–144 ms. Every VU
  connects and loads metadata at once at the start. Whether the wait is
  in kfake or in the client was not measured, and a real broker may
  behave differently.
- **There is one outlier.** At 100 VUs, run 3 peaked at 324 MB against
  191–194 MB in the other runs, with the same throughput. It is reported
  as measured.

## Caveats

- **The broker is kfake, in process, on the same machine.** These are
  not Kafka numbers, and they include none of a real broker's disk,
  replication or network costs. A real broker is tested for correctness
  only (CI's `kafka-real` job).
- **Latencies below about 0.6 ms are quantized.** On this machine Go's
  monotonic clock advances in steps of 0.50–0.63 ms (measured
  2026-10-09), so the 10-VU p50 (0.54 ms) is one clock tick, and
  sub-millisecond percentiles are not precise. Throughput and memory are
  not affected.
- **Background CPU from the security agents** was present throughout and
  rose after the runs.
- **One laptop, three runs per level.**
