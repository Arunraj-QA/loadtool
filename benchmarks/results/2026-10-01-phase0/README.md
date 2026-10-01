# 2026-10-01 — Phase 0 comparison: LoadTool and k6 (JMeter not measured)

> **Superseded for the Phase 0 question.** JMeter was measured later the
> same day, together with LoadTool and k6, in
> [2026-10-01-phase0-all-tools](../2026-10-01-phase0-all-tools/README.md).
> This report stays as recorded.

**Status: incomplete.** LoadTool and k6 were measured at all five VU
levels. **JMeter was not measured**: it could not be downloaded in time
(see [Missing results](#missing-results)). The Phase 0 question compares
LoadTool's memory with JMeter's, so **this session cannot answer it**.

Everything in [Measured values](#measured-values) and
[Calculated values](#calculated-values) comes from
[`raw/runs.jsonl`](raw/runs.jsonl). The tables are generated from that
file by `benchmarks/summarize.ps1` ([`raw/summary.md`](raw/summary.md),
[`raw/summary.csv`](raw/summary.csv)). Nothing was estimated or filled in.

## Environment

Recorded automatically in [`raw/environment.json`](raw/environment.json).

| Item | Value |
|---|---|
| Machine | Laptop, one machine for both the server and the load tools |
| CPU | 12th Gen Intel Core i5-1235U: 10 cores, 12 logical CPUs |
| RAM | 15.7 GB |
| OS | Windows 11 Pro 10.0.26200 (build 26200) |
| Power | On AC power |
| Background activity | A slow JMeter download (`curl`, about 4 KB/s) ran for the whole session |

### Tool versions

| Tool | Version |
|---|---|
| LoadTool | commit `eda6302`, built with go1.27.0 windows/amd64 |
| k6 | v1.7.1 (commit 9f82e6f1fc, go1.26.1, windows/amd64) |
| JMeter | **not installed**; Java present: OpenJDK 17.0.19 |
| Benchmark server | commit `eda6302`, built with go1.27.0 |

All tools used default settings (`GOGC`, `GOMEMLIMIT` and k6 options), as
set in the committed scenario files.

## Test configuration

| Setting | Value |
|---|---|
| VU levels | 100, 250, 500, 750, 1000 |
| Measured run length | 60 s |
| Repetitions | 3 measured runs per tool per VU level (30 measured runs) |
| Warm-up | 1 × 30 s per tool per VU level, discarded (recorded in `runs.jsonl` with `kind: warmup`) |
| Order | Per VU level: warm-ups, then LoadTool, k6, LoadTool, k6, LoadTool, k6 |
| Cool-down | 5 s between runs |
| Scenario | `GET http://127.0.0.1:8080/api/test`. All VUs start at once, no think time, keep-alive on, redirects not followed, response bodies not kept for the script. Files: `benchmarks/loadtool/scenario.ts`, `benchmarks/k6/scenario.js` |

### Target server

`benchmarks/server` (Go standard library only) at commit `eda6302`.
- Listening on `127.0.0.1:8080`, with `-delay 10ms`.
- `GET /api/test` returns `200` with the same 66-byte JSON body, after
  10 ms.
- It ran **on the same machine** as the load tools and was started and
  stopped by the harness.

Command:

```powershell
./benchmarks/measure.ps1 -Tools loadtool,k6 -VUs 100,250,500,750,1000 -DurationSec 60 -Runs 3 -WarmupSec 30 -OutDir benchmarks/results/2026-10-01-phase0/raw
```

## How metrics were collected

| Metric | Type | Source |
|---|---|---|
| Peak memory | measured | Highest private bytes of the tool's process, sampled every 250 ms (`*-memory.csv`) |
| Requests, errors | measured | LoadTool console summary; k6 `--summary-export` (`http_reqs.count`, `http_req_failed.passes`) |
| Requests/sec | measured | Reported by each tool. LoadTool: requests ÷ its own elapsed time. k6: `http_reqs.rate`. |
| p50, p95, p99 | measured | Reported by each tool. LoadTool uses nearest rank; k6 interpolates. |
| Process CPU time and wall time | measured | `TotalProcessorTime` and `ExitTime − StartTime` of the tool's process |
| CPU % | calculated | CPU time ÷ wall time ÷ 12 logical CPUs, over the whole process lifetime (start-up included) |
| Average memory | calculated | Mean of the 250 ms private-bytes samples over the process lifetime |
| Error rate | calculated | errors ÷ requests × 100 |
| Server CPU % | calculated | Server CPU time during the run ÷ wall time ÷ 12 logical CPUs |

The measured k6 process is the real `k6.exe`, not the Chocolatey launcher.

## Measured values

Median (min–max) of 3 runs.

### Peak memory (MB)

| VUs | LoadTool | k6 |
|---|---|---|
| 100 | 75.1 (73.0–78.3) | 188 (183–192) |
| 250 | 101 (94.6–108) | 318 (314–382) |
| 500 | 134 (127–136) | 409 (383–415) |
| 750 | 171 (168–179) | 493 (470–512) |
| 1000 | 218 (214–233) | 566 (522–582) |

### Requests/sec

| VUs | LoadTool | k6 |
|---|---|---|
| 100 | 8,638 (8,117–8,947) | 8,338 (8,021–8,674) |
| 250 | 13,043 (13,002–17,586) | 16,178 (15,319–17,604) |
| 500 | 16,463 (14,077–17,981) | 14,536 (13,304–15,186) |
| 750 | 21,366 (11,873–21,687) | 16,983 (16,415–18,628) |
| 1000 | 19,330 (15,970–24,254) | 13,705 (12,388–16,651) |

### Latency (ms)

| VUs | p50 LoadTool | p50 k6 | p95 LoadTool | p95 k6 | p99 LoadTool | p99 k6 |
|---|---|---|---|---|---|---|
| 100 | 11.2 (10.9–11.5) | 11.4 (11.2–11.5) | 13.9 (12.8–16.2) | 15.0 (13.5–16.5) | 17.2 (14.7–25.8) | 19.7 (16.0–23.8) |
| 250 | 14.2 (12.2–15.3) | 13.6 (12.9–14.0) | 34.4 (23.2–41.1) | 24.1 (20.6–26.9) | 89.7 (46.7–93.0) | 41.6 (31.5–51.7) |
| 500 | 22.2 (20.2–26.0) | 23.9 (23.7–26.6) | 71.1 (65.9–81.6) | 81.9 (70.4–85.5) | 128 (114–142) | 163 (151–173) |
| 750 | 27.8 (26.7–40.6) | 32.9 (30.5–34.1) | 76.5 (68.7–142) | 94.4 (84.7–97.1) | 147 (104–274) | 172 (155–186) |
| 1000 | 39.5 (30.1–40.0) | 41.2 (35.8–49.8) | 114 (91.7–178) | 170 (120–209) | 180 (173–301) | 303 (224–410) |

### Errors (count)

| VUs | LoadTool | k6 |
|---|---|---|
| 100 | 0 (0–0) | 0 (0–0) |
| 250 | 41 (0–140) | 15 (0–33) |
| 500 | 875 (861–890) | 277 (133–287) |
| 750 | 3,356 (1,418–4,623) | 567 (484–709) |
| 1000 | 5,729 (5,535–10,570) | 1,061 (998–1,270) |

Totals over all measured runs:
- LoadTool: 34,038 errors in 13,959,803 requests.
- k6: 5,834 errors in 12,722,752 requests.

## Calculated values

Median (min–max) of 3 runs.

### CPU % (share of the whole 12-CPU machine)

| VUs | LoadTool | k6 |
|---|---|---|
| 100 | 11.1 (9.64–14.2) | 17.9 (15.7–18.3) |
| 250 | 18.6 (15.1–23.0) | 35.3 (27.7–37.0) |
| 500 | 24.9 (23.6–25.9) | 31.2 (30.8–33.7) |
| 750 | 26.2 (20.0–26.9) | 33.3 (33.2–36.7) |
| 1000 | 27.4 (24.3–33.7) | 32.0 (29.9–32.0) |

### Average memory (MB)

| VUs | LoadTool | k6 |
|---|---|---|
| 100 | 68.7 (68.6–68.7) | 136 (135–138) |
| 250 | 86.5 (85.7–88.0) | 217 (213–227) |
| 500 | 121 (116–122) | 297 (286–301) |
| 750 | 157 (145–160) | 377 (376–378) |
| 1000 | 195 (192–199) | 443 (442–458) |

### Error rate (%)

| VUs | LoadTool | k6 |
|---|---|---|
| 100 | 0.00 | 0.00 |
| 250 | 0.00 (0.00–0.02) | 0.00 (0.00–0.00) |
| 500 | 0.09 (0.08–0.11) | 0.03 (0.02–0.04) |
| 750 | 0.26 (0.11–0.65) | 0.06 (0.05–0.06) |
| 1000 | 0.58 (0.49–0.72) | 0.12 (0.11–0.17) |

### Server CPU % during the run

| VUs | with LoadTool | with k6 |
|---|---|---|
| 100 | 10.5 (9.87–13.0) | 11.2 (10.2–11.2) |
| 250 | 16.2 (13.7–21.5) | 20.9 (16.3–21.2) |
| 500 | 21.8 (20.5–23.0) | 16.6 (16.5–18.1) |
| 750 | 22.5 (15.6–22.8) | 17.9 (17.9–18.7) |
| 1000 | 23.0 (19.2–25.8) | 15.9 (14.7–16.3) |

## Observations

These are interpretations of the data above, with their limits stated.

1. **LoadTool completed all three 1,000-VU runs.** All runs exited with
   code 0 and LoadTool did not crash. (An earlier attempt crashed with
   thread exhaustion before commit `008a76f`.)
2. **Memory:** LoadTool's peak and average private memory were lower than
   k6's at every VU level. The min–max ranges do not overlap at any level.
   - At 1,000 VUs: peak 218 MB against 566 MB, average 195 MB against
     443 MB.
   - This is a LoadTool-to-k6 comparison on this machine only. It says
     nothing about JMeter.
3. **Every observed error was a refused connection from the local
   server.** At 250 VUs and above, both tools got errors.
   - k6 logs each failure: all 5,834 were `connectex: ... actively
     refused`.
   - LoadTool reports only the first error message per run. In all 11 runs
     with errors, it was the same refused-connection error. LoadTool does
     not record per-error causes, so the rest are not individually
     confirmed.
   - This matches the known Windows accept-backlog limit with server and
     tools on one machine. The methodology treats runs with target errors
     as measuring the environment, so **throughput and latency at
     ≥250 VUs are not a valid tool comparison**.
4. **LoadTool recorded more errors than k6 at ≥250 VUs** (e.g. 5,729
   against 1,061 at 1,000 VUs). The cause was not investigated. One
   possibility, untested, is that the two tools retry differently after a
   refused connection.
5. **Throughput was well below what the server's 10 ms delay allows**
   (VUs ÷ 10 ms, e.g. 100,000 req/s at 1,000 VUs) from 250 VUs upwards.
   - The server and the tools shared one 12-CPU laptop.
   - It was not determined whether the server, the tools, or the OS
     network stack was the limit.
6. **Run-to-run variation is large at ≥500 VUs.** For example, LoadTool
   at 750 VUs ranged from 11,873 to 21,687 req/s. Three runs are not
   enough to rank the tools on throughput or latency at these levels.
7. **CPU % is not directly comparable between the tools at ≥250 VUs.**
   They completed different numbers of requests, and CPU % includes
   process start-up.
8. **Percentile methods differ** (nearest rank against interpolation), so
   small latency differences between tools are not meaningful.

## Missing results

- **JMeter (all VU levels): not measured.**
  - This network downloads from the Apache distribution servers and
    mirrors at about 4 KB/s. The official 90.6 MB
    `apache-jmeter-5.6.3.zip` had reached about 2 % after more than an
    hour.
  - The official SHA-512 was saved for verification once the file
    arrives.
  - The harness supports JMeter (`-Tools jmeter`), but its JMeter path
    has not been run yet.
- **Two-machine setup:** not used. The methodology recommends a separate
  server machine; this session used one laptop.

## Phase 0 question

> Can LoadTool reach 1,000 VUs while keeping memory lower than JMeter at
> the same VU count?

- **Reaching 1,000 VUs:** LoadTool ran 1,000 VUs for 60 s in three runs
  without crashing. The runs had a 0.49–0.72 % error rate, from refused
  connections at the local server.
- **Memory against JMeter: unanswered.** JMeter was not measured.

The Phase 0 exit criterion is **not met** by this session.

## Reproducing

```powershell
go build -o bin/loadtool.exe ./cmd/loadtool
go build -o bin/benchserver.exe ./benchmarks/server
./benchmarks/measure.ps1 -Tools loadtool,k6 -VUs 100,250,500,750,1000 -OutDir benchmarks/results/<new-name>/raw
./benchmarks/summarize.ps1 -Runs benchmarks/results/<new-name>/raw/runs.jsonl
```
