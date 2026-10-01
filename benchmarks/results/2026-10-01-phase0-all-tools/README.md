# 2026-10-01 — Phase 0 comparison: LoadTool, k6 and JMeter

All three tools were measured in **one session**, alternating at every VU
level.

Data:
- Every number below comes from [`raw/runs.jsonl`](raw/runs.jsonl).
- The tables are generated from it by `benchmarks/summarize.ps1`
  ([`raw/summary.md`](raw/summary.md), [`raw/summary.csv`](raw/summary.csv)).
- Nothing was estimated or filled in. All 45 measured runs exited with
  code 0, and no metric is missing.

## Environment

Recorded automatically in [`raw/environment.json`](raw/environment.json).

| Item | Value |
|---|---|
| Machine | Laptop. The benchmark server and the load tools ran **on the same machine**. |
| CPU | 12th Gen Intel Core i5-1235U: 10 cores, 12 logical CPUs |
| RAM | 15.7 GB |
| OS | Windows 11 Pro 10.0.26200 (build 26200) |
| Power | On AC power |
| Session | Measured runs from 14:37 to 17:03 local time |

### Tool versions

| Tool | Version and settings |
|---|---|
| LoadTool | commit `3b7b523`, go1.27.0 windows/amd64. Default settings. |
| k6 | v1.7.1 (commit 9f82e6f1fc, go1.26.1). Default settings plus the scenario's options. |
| JMeter | Apache JMeter 5.6.3 on OpenJDK 17.0.19, non-GUI, launched with `bin\jmeter.bat`. Default JVM settings: `-Xms1g -Xmx1g -XX:MaxMetaspaceSize=256m`, G1 GC. |
| Benchmark server | commit `3b7b523`, go1.27.0 |

#### JMeter installation

- **Source:** a local JMeter 5.6.3 install provided by the user.
- **Verified:** `ApacheJMeter.jar`, `ApacheJMeter_core.jar`,
  `ApacheJMeter_http.jar` and `ApacheJMeter_components.jar` match the
  SHA-1 checksums Apache publishes for 5.6.3 on Maven Central.
- **Changes in the copy used for the runs** (the user's install was not
  modified):
  - Removed 4 third-party plugins from `lib/ext`: `jmeter-agent-3.8.0`,
    `jmeter-plugins-esa-0.1`, `jmeter-plugins-json-2.7` and
    `jmeter-plugins-manager-1.12`.
  - Replaced `bin/jmeter.properties` and `bin/system.properties` with the
    official 5.6.3 files. The install had one extra line,
    `jmeter.ai.service.type=ollama`.
  - Reason: plugins load at start-up and add memory and CPU. Leaving them
    in would have biased the memory comparison against JMeter.
- **Other checks:** no `bin/setenv.bat`; `user.properties` had only
  comments.

## Test configuration

| Setting | Value |
|---|---|
| VU levels | 100, 250, 500, 750, 1000 (JMeter threads = VUs) |
| Measured run length | 60 s |
| Repetitions | **3 measured runs per tool per VU level**: 45 measured runs |
| Warm-up | 1 × 30 s per tool per VU level, discarded (in `runs.jsonl` as `kind: warmup`) |
| Order | Per VU level: warm-ups, then LoadTool → k6 → JMeter, repeated 3 times |
| Cool-down | 5 s between runs |
| Workload | `GET http://127.0.0.1:8080/api/test`. All VUs start at once, no ramp-up, no think time, keep-alive on, redirects not followed. Success = status 200. Response bodies not kept for the script (k6 `discardResponseBodies`, JMeter MD5-only). |
| Scenario files | `benchmarks/loadtool/scenario.ts`, `benchmarks/k6/scenario.js`, `benchmarks/jmeter/scenario.jmx` |
| JMeter result file | CSV JTL reduced to `timeStamp`, `elapsed`, `responseCode` and `success`, which lowers JMeter's disk work |

### Target server

[`benchmarks/server`](../../server/README.md) at commit `3b7b523`, using
only the Go standard library.
- Run as `bin/benchserver.exe -addr 127.0.0.1:8080 -delay 10ms`, started
  and stopped by the harness.
- `GET /api/test` returns `200 application/json` with a fixed 66-byte
  body, after a fixed 10 ms delay.
- Plain `net/http`, not tuned for any client.

Command:

```powershell
./benchmarks/measure.ps1 -Tools loadtool,k6,jmeter -VUs 100,250,500,750,1000 -DurationSec 60 -Runs 3 -WarmupSec 30 `
  -OutDir benchmarks/results/2026-10-01-phase0-all-tools/raw -JMeterHome <clean JMeter copy>
```

## How metrics were collected

| Metric | Type | Source |
|---|---|---|
| Peak private bytes | measured | Highest `PrivateMemorySize64` of the tool's process (`java.exe` for JMeter), sampled every 250 ms (`raw/*-memory.csv`) |
| Peak working set | measured | Highest `WorkingSet64` from the same samples |
| Requests, errors, requests/sec | measured | Each tool's own report. LoadTool: console summary. k6: `--summary-export`. JMeter: final `summary =` line. JMeter's counts were cross-checked against its JTL and matched in all 15 runs. |
| p50, p95, p99: LoadTool, k6 | measured | Reported by the tool |
| p50, p95, p99: JMeter | calculated | Nearest rank over the JTL `elapsed` column (whole ms). JMeter's non-GUI summary has no percentiles. |
| Process CPU time and wall time | measured | `TotalProcessorTime`, `ExitTime − StartTime` |
| CPU % | calculated | CPU time ÷ wall time ÷ 12 logical CPUs, over the process lifetime, start-up included |
| Average private bytes / working set | calculated | Mean of the 250 ms samples over the process lifetime |
| Error rate | calculated | errors ÷ requests × 100 |
| Server CPU % | calculated | Server CPU time during the run ÷ wall time ÷ 12 logical CPUs |

**Two memory measures** are reported because they diverge for JMeter:
- **Private bytes** is memory committed to the process. JMeter's default
  `-Xms1g` commits a 1 GB heap at start-up.
- **Working set** is the part of the process's memory actually in RAM.

## Measured values

Median (min–max) of 3 runs.

### Peak private bytes (MB)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 73.2 (70.2–76.6) | 171 (153–174) | 1,268 (1,265–1,273) |
| 250 | 96.3 (94.8–100) | 264 (258–265) | 1,274 (1,271–1,296) |
| 500 | 133 (115–140) | 340 (312–355) | 1,312 (1,306–1,319) |
| 750 | 171 (165–175) | 407 (407–425) | 1,342 (1,335–1,375) |
| 1000 | 196 (190–210) | 451 (444–481) | 1,368 (1,366–1,371) |

### Peak working set (MB)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 38.7 (36.4–42.6) | 146 (128–148) | 764 (763–798) |
| 250 | 113 (111–116) | 237 (231–238) | 796 (790–802) |
| 500 | 147 (80.1–153) | 311 (284–327) | 855 (849–868) |
| 750 | 182 (177–182) | 378 (378–392) | 902 (882–939) |
| 1000 | 150 (135–215) | 421 (414–449) | 966 (917–975) |

### Requests/sec

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 7,018 (4,564–9,234) | 5,743 (3,044–6,116) | 7,584 (5,975–7,959) |
| 250 | 13,889 (13,502–14,061) | 10,859 (10,284–10,979) | 10,776 (9,443–11,203) |
| 500 | 11,094 (5,843–14,986) | 9,817 (8,373–10,759) | 8,356 (7,869–10,582) |
| 750 | 13,006 (12,248–13,381) | 9,414 (8,081–11,065) | 8,622 (8,201–10,329) |
| 1000 | 6,648 (4,804–14,088) | 6,211 (5,887–10,516) | 5,781 (4,795–6,693) |

### Latency (ms)

JMeter values are calculated from its JTL in whole milliseconds (see
above).

| VUs | p50 LoadTool | p50 k6 | p50 JMeter | p95 LoadTool | p95 k6 | p95 JMeter | p99 LoadTool | p99 k6 | p99 JMeter |
|---|---|---|---|---|---|---|---|---|---|
| 100 | 12.7 | 14.0 | 12.0 | 20.6 | 25.6 | 19.0 | 34.8 | 48.5 | 41.0 |
| 250 | 15.9 | 18.6 | 18.0 | 31.4 | 43.3 | 48.0 | 41.6 | 94.2 | 96.0 |
| 500 | 34.8 | 39.9 | 40.0 | 92.9 | 111 | 162 | 141 | 215 | 340 |
| 750 | 45.5 | 58.2 | 55.0 | 104 | 184 | 234 | 154 | 320 | 519 |
| 1000 | 103 | 104 | 81.0 | 332 | 393 | 368 | 534 | 651 | 881 |

These are medians. Min–max ranges are in [`raw/summary.md`](raw/summary.md);
several overlap widely. For example, LoadTool p99 at 1,000 VUs ranged
from 195 to 880 ms.

### Errors (count)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 0 (0–0) | 0 (0–0) | 0 (0–0) |
| 250 | 154 (0–786) | 10 (0–22) | 48 (46–112) |
| 500 | 2,398 (159–4,198) | 145 (133–255) | 498 (489–1,004) |
| 750 | 5,099 (4,200–13,126) | 411 (365–488) | 763 (755–1,509) |
| 1000 | 2,246 (897–6,189) | 1,115 (617–1,615) | 10,802 (1,649–25,421) |

Totals over all measured runs:
- LoadTool: 39,452 errors in 9,518,165 requests.
- k6: 5,176 errors in 7,727,396 requests.
- JMeter: 43,096 errors in 7,795,256 requests.

## Calculated values

Median (min–max) of 3 runs.

### Average private bytes (MB)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 67.8 (66.9–69.4) | 124 (117–124) | 1,215 (1,211–1,217) |
| 250 | 87.5 (86.0–89.0) | 197 (192–199) | 1,234 (1,228–1,236) |
| 500 | 121 (110–125) | 274 (260–279) | 1,265 (1,259–1,267) |
| 750 | 152 (150–156) | 345 (341–348) | 1,277 (1,269–1,309) |
| 1000 | 178 (169–190) | 398 (395–423) | 1,300 (1,287–1,309) |

### Average working set (MB)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 33.3 (32.7–35.1) | 97.1 (89.0–97.4) | 681 (661–701) |
| 250 | 62.3 (61.3–66.0) | 169 (165–172) | 719 (698–728) |
| 500 | 86.1 (73.3–97.0) | 245 (232–248) | 726 (724–745) |
| 750 | 122 (120–126) | 313 (311–318) | 734 (728–816) |
| 1000 | 130 (119–152) | 365 (364–391) | 772 (705–830) |

### CPU % (share of the whole 12-CPU machine)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 12.4 (9.05–17.0) | 17.3 (9.02–17.8) | 19.6 (13.3–20.9) |
| 250 | 26.0 (25.2–26.6) | 29.7 (28.9–33.0) | 26.7 (24.4–27.2) |
| 500 | 26.0 (16.0–28.5) | 32.8 (32.4–33.9) | 27.0 (26.8–29.3) |
| 750 | 29.9 (28.6–31.3) | 31.9 (31.8–35.1) | 28.5 (27.0–28.8) |
| 1000 | 22.5 (12.0–31.0) | 28.5 (25.1–35.8) | 31.3 (23.4–31.9) |

### Error rate (%)

| VUs | LoadTool | k6 | JMeter |
|---|---|---|---|
| 100 | 0.00 | 0.00 | 0.00 |
| 250 | 0.02 (0.00–0.10) | 0.00 (0.00–0.00) | 0.01 (0.01–0.02) |
| 500 | 0.36 (0.05–0.47) | 0.02 (0.02–0.05) | 0.10 (0.08–0.20) |
| 750 | 0.65 (0.52–1.78) | 0.08 (0.05–0.09) | 0.14 (0.14–0.24) |
| 1000 | 0.73 (0.22–0.77) | 0.30 (0.10–0.46) | 2.36 (0.48–5.68) |

### Server CPU % during the run

| VUs | with LoadTool | with k6 | with JMeter |
|---|---|---|---|
| 100 | 12.7 (8.39–15.9) | 10.5 (5.75–10.5) | 15.9 (8.25–16.0) |
| 250 | 23.4 (22.7–23.6) | 16.4 (15.9–18.9) | 19.6 (16.8–20.8) |
| 500 | 22.4 (12.5–24.0) | 17.4 (17.1–18.2) | 18.4 (18.3–19.7) |
| 750 | 22.8 (20.4–23.4) | 16.6 (16.4–18.6) | 18.4 (18.1–20.0) |
| 1000 | 17.8 (8.02–23.6) | 14.6 (12.7–18.6) | 10.6 (10.6–14.9) |

## Observations

These are interpretations of the data above, with their limits stated.

1. **Memory is the one clear result.** At every VU level, LoadTool's
   memory was lower than both k6's and JMeter's on all four memory
   measures (peak and average private bytes, peak and average working
   set). The min–max ranges do not overlap in any of these comparisons:
   in all 40 (5 levels × 4 measures × 2 tools), LoadTool's highest run was
   below the other tool's lowest run.
   At 1,000 VUs:

   | Measure | LoadTool | k6 | JMeter |
   |---|---|---|---|
   | Peak private bytes | 196 (190–210) MB | 451 (444–481) MB | 1,368 (1,366–1,371) MB |
   | Peak working set | 150 (135–215) MB | 421 (414–449) MB | 966 (917–975) MB |
   | Average working set | 130 (119–152) MB | 365 (364–391) MB | 772 (705–830) MB |

2. **JMeter's private bytes reflect its default heap setting** (`-Xms1g`).
   It stays near 1.2–1.4 GB at every VU level. Its working set, the memory
   actually in RAM, is lower but grows with VUs (681 → 772 MB average).
   - The comparison does not depend on which measure is used: LoadTool is
     lower on both.
   - JMeter with a smaller configured heap was **not** measured. Its
     private bytes would be different, and whether it would still run
     1,000 threads is unknown.
3. **All observed errors were connection failures at the local server.**
   - k6 logged each failure: all 5,176 were `connectex: ... actively
     refused`.
   - JMeter's JTL recorded all 43,096 failures as
     `Non HTTP response code: org.apache.http.conn.HttpHostConnectException`,
     i.e. failed TCP connects. The JTL does not keep the message text.
   - LoadTool reports only its first error message per run. In all 11 runs
     with errors, it was `connectex: ... actively refused`.
   - This is the known Windows accept-backlog behaviour with server and
     load generator on one machine. The methodology treats such runs as
     measuring the environment, so **throughput and latency at ≥250 VUs
     are not a valid tool comparison**.
4. **Throughput and latency vary widely between runs and between
   sessions.**
   - For example, LoadTool at 1,000 VUs ranged from 4,804 to 14,088 req/s
     in this session.
   - The earlier same-day LoadTool and k6 session
     ([2026-10-01-phase0](../2026-10-01-phase0/README.md)) measured
     15,970–24,254 req/s for the same configuration.
   - These numbers are too unstable to rank the tools on throughput or
     latency.
5. **Throughput was far below what the server's 10 ms delay allows**
   (VUs ÷ 10 ms) from 250 VUs up. All processes shared one 12-CPU laptop.
   Which component was the limit was not determined.
6. **Error counts differ by tool and are not explained.** By median,
   LoadTool had the most errors at 250, 500 and 750 VUs, and JMeter had
   the most at 1,000 VUs. The tools' retry behaviour after a refused
   connection was not investigated.
7. **CPU % is not comparable between tools at ≥250 VUs.** The tools
   completed different numbers of requests, and CPU % includes start-up,
   which is longer for the JVM.
8. **Percentile methods differ:** LoadTool and the JMeter calculation use
   nearest rank, and k6 interpolates. JMeter has whole-millisecond
   resolution. Small latency differences are not meaningful.

## Phase 0 question

> Can LoadTool reach 1,000 VUs while keeping memory lower than JMeter at
> the same VU count?

**Measured answer for this machine and configuration: yes.**

- **Reaching 1,000 VUs:** LoadTool completed all three 60 s runs at
  1,000 VUs (exit code 0). The error rate was 0.22–0.77 %, all from
  connection failures at the local server (observation 3).
- **Memory against JMeter at 1,000 VUs:** LoadTool was lower on every
  measure, with no overlap between runs:
  - peak private bytes: 190–210 MB against 1,366–1,371 MB
  - peak working set: 135–215 MB against 917–975 MB
  - average working set: 119–152 MB against 705–830 MB

**Limits of this answer:**
- It was measured with the server on the same laptop. The methodology
  prefers a separate server machine, which would also remove the
  connection errors.
- JMeter ran with its default JVM settings. A JMeter tuned for low
  memory was not tested.
- This result is about memory only. It shows nothing about throughput or
  latency (observations 3–5).

## Raw data

- `raw/runs.jsonl`: every run, warm-ups included.
- `raw/environment.json`
- Per run: the tool's stdout and stderr, `*-memory.csv` (250 ms samples),
  the k6 summary JSON, the JMeter log, and the LoadTool scenario copy.
- **JMeter JTL files** (9.5–18.2 MB each) are kept outside the repository
  in `bench-out/jtl/`. Each one's size and SHA-256 are recorded in
  `runs.jsonl` under `jtl`.

## Reproducing

```powershell
go build -o bin/loadtool.exe ./cmd/loadtool
go build -o bin/benchserver.exe ./benchmarks/server
./benchmarks/measure.ps1 -JMeterHome <JMeter 5.6.3 without plugins> -OutDir benchmarks/results/<new-name>/raw
./benchmarks/summarize.ps1 -Runs benchmarks/results/<new-name>/raw/runs.jsonl
```
