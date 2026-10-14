# 2026-10-14 — Phase 2 benchmark session

This session measures Phase 2 exit criteria 2 to 4, recorded in
`CLAUDE.md` on 2026-10-08 before any of it ran. It also runs each protocol
at several load levels. Criterion 1 (mixed protocols work end to end) is
met by CI; see `docs/mixed-protocols.md`.

| Criterion | Result |
|---|---|
| 2. No HTTP regression against the Phase 1 build; memory below JMeter's | **Met.** Memory, CPU and requests/s are within run-to-run variation; peak memory 176–193 MB against JMeter's 1,366–1,399 MB |
| 3. No per-VU cost for unused modules | **Not met as worded.** Unused modules cost nothing, but every Phase 2 VU retains 48 B more than Phase 1's, script imports or not ([§3](#3-exit-criterion-3-retained-memory-per-vu)) |
| 4. Each protocol measured at 1,000 VUs | **Met.** HTTP, WebSocket, gRPC, GraphQL and Kafka, below |

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`powerOnline: true`, checked before each part); no other work was started during the runs |
| Background load | The corporate security agents were busy throughout. Total CPU was 5–36 % before the runs and 32–71 % right after them (`raw/environment*.json`, `raw/session*.log`) |
| Targets | All on the **same machine**: `benchmarks/server` (HTTP), and the demo API for the protocols (`bin/demo-api.exe -delay 10ms`, a fresh process per run). Kafka is the demo's in-process broker (franz-go's kfake), **not a real Kafka** |
| LoadTool | Phase 2 at `3d1c14d`; Phase 1 build `cf35298`, built from a worktree |
| JMeter | 5.6.3, the clean copy without plugins used on 2026-10-01 and 2026-10-07, default JVM settings |
| Go | go1.27.0 windows/amd64 |

**Method.** `run.ps1` in this folder ran everything. The regression check
(`-Sections ab`) ran first, alone, so that an HTTP regression could be
investigated before the rest; the other sections followed in one go
(20:38–21:32).

**One failed attempt.** The first A/B run measured nothing. The script
path contains spaces and was not quoted, so every run exited at once with
an argument error. Its output was deleted, the quoting fixed and checked
on a 3 s run, and the A/B run again. Only the second attempt is recorded.

**Clock resolution.** On this machine Go's monotonic clock advances in
steps of about 0.5–0.6 ms, so latencies below that are quantized: p50s of
0.00 ms below are sub-tick, not zero. Throughput, memory and CPU are not
affected.

## 1. Exit criterion 2: Phase 1 build against Phase 2, HTTP at 1,000 VUs

The same scenario (`benchmarks/loadtool/scenario.ts`, one `GET /api/test`
per iteration, 10 ms server delay, bodies discarded). Each run lasted
60 s, over three rounds, with the order alternating each round and peaks
sampled every 250 ms. Raw rows are in `raw/ab-phase1-vs-phase2.csv`.

The rule for "within run-to-run variation" was set before the runs: the
two builds' ranges overlap, and the medians differ by no more than the
wider range.

| Build | Peak private MB | CPU % of machine | Requests/s | Errors |
|---|---|---|---|---|
| Phase 1 (`cf35298`) | 176.1 (168.2–176.8) | 43.9 (40.8–45.3) | 48,160 (44,655–56,518) | 0.04–0.16 % |
| Phase 2 (`3d1c14d`) | 177.6 (167.9–189.4) | 46.2 (45.3–47.0) | 44,731 (40,903–54,912) | 0.04–0.08 % |
| Difference of medians | +1.5 (wider range 21.5) | +2.3 (wider range 4.5) | −3,429 (wider range 14,009) | |

**All three are within run-to-run variation by that rule.** Two caveats:

- **Requests/s drifted** for both builds, from about 55,000 in round 1 to
  about 45,000 in round 3, while background CPU rose. With this much noise,
  three rounds rule out only a large regression (roughly 25 % or more).
- **CPU leans higher for Phase 2.** Every Phase 2 run (45.3–47.0 %) was at
  or above Phase 1's highest (45.3 %). That is within the rule, but it is
  the measure to watch. More alternating rounds would settle it.

### Memory against JMeter, at 1,000 VUs

`benchmarks/measure.ps1` ran with the Phase 0 and Phase 1 settings: a 30 s
warm-up per tool, then three rounds of 60 s runs.

| Tool | Peak private MB | Avg private MB | CPU % | Requests/s | p95 ms | Errors |
|---|---|---|---|---|---|---|
| LoadTool (Phase 2) | **175.9** (175.8–193.0) | 166.4 (165.2–182.3) | 44.3 (43.9–44.7) | 43,862 (37,782–44,633) | 41.2 (40.1–50.6) | 0.08–0.20 % |
| JMeter | 1,383.9 (1,366.3–1,398.7) | 1,325.6 (1,317.5–1,343.8) | 47.4 (46.8–48.0) | 37,649 (36,081–37,653) | 59 (58–64) | 0.06–0.07 % |

**LoadTool's peak memory was below JMeter's on every run.** The latency
and throughput columns describe this setup only, not a tool comparison:
one laptop runs the tools and the target, and the tools measure latency
slightly differently.

## 2. Protocols at several levels

Each level got one warm-up, then three 30 s runs, with the levels
alternating within each round and a fresh server per run. The tables
give medians with ranges.

### HTTP (`benchmarks/measure.ps1`, `benchmarks/server`, 10 ms delay)

| VUs | Requests/s | p50 ms | p95 ms | p99 ms | Errors | Peak private MB | CPU % |
|---|---|---|---|---|---|---|---|
| 100 | 9,232 (9,205–9,234) | 10.68 | 11.86 (11.86–12.12) | 13.70 (13.57–14.09) | 0 % | 67.7 (67.4–68.0) | 12.5 (12.1–15.8) |
| 500 | 40,648 (40,481–41,313) | 11.60 (11.47–11.60) | 16.45 (15.93–16.71) | 20.32 (19.79–20.84) | 0.04 % | 111.5 (110.4–113.1) | 33.2 (32.4–34.5) |
| 1,000 | 48,708 (46,787–49,698) | 17.96 (17.69–18.74) | 35.39 (34.87–36.96) | 46.40 (46.40–51.12) | 0.10 % | 175.6 (172.8–180.9) | 45.1 (44.6–45.8) |
| 5,000 | 33,977 (29,956–42,966) | 119.01 (99.09–128.45) | 256.90 (210.76–287.31) | 484.44 (325.06–708.84) | 0.73 % | 617.2 (607.8–649.1) | 42.3 (42.1–46.7) |

At 5,000 VUs this laptop is past capacity: throughput falls and latency
grows ten-fold. The errors are refused connections; see the note on
connection storms below.

### WebSocket (`benchmarks/protocol.ps1`, `benchmarks/loadtool/websocket.ts`)

Each VU holds one connection for the whole run and sends one 64-byte
message a second, timing its echo, so connections equal VUs. The echo
answers after 10 ms.

| Connections | Messages/s | Echo p50 ms | p95 ms | p99 ms | Lost messages | Failed connects (retried) | LoadTool peak MB | CPU % |
|---|---|---|---|---|---|---|---|---|
| 100 | 97 | 10.55 (10.42–10.55) | 10.81 (10.81–10.94) | 11.21 (11.21–11.60) | 0 | 0 | 72.7 (71.9–72.8) | 0.2 |
| 500 | 484 | 10.42 | 10.81 (10.68–10.81) | 11.21 (10.94–12.52) | 0 | 1,083 (996–1,563) | 127.9 (126.4–133.9) | 1.1 (0.9–1.2) |
| 1,000 | 966 | 10.42 | 10.94 (10.81–11.08) | 11.73 (11.08–12.65) | 0 | 4,500 (3,620–7,415) | 204.1 (201.5–212.2) | 2.0 (1.8–2.5) |
| 5,000 | 4,622 (4,497–4,642) | 10.42 | 11.60 (11.47–11.86) | 95.94 (91.75–170.92) | 2–67 of 140,000–145,000 | 26,232 (23,172–38,775) | 778.5 (771.0–785.1) | 11.4 (10.1–11.9) |

- **Every connection was established and held.** All expected messages
  were sent and echoed (only at 5,000 did a few, 2 to 67 per run, lack an
  echo when the run ended).
- **"Failed connects" are refused TCP connections** during the opening
  burst, retried at once by the same VU. A diagnostic run showed every one
  as `error_code` `dial`, "actively refused", within about 2 ms.
- **Held connections cost about 0.15 MB each** in LoadTool: 778 MB at
  5,000 against 73 MB at 100.

### gRPC (`benchmarks/grpc-unary.ps1`, demo greeter, 10 ms delay)

| VUs | Calls/s | p50 ms | p95 ms | p99 ms | Failed calls | Failed connects | Peak private MB | CPU % |
|---|---|---|---|---|---|---|---|---|
| 100 | 8,982 (7,088–9,269) | 10.94 (10.68–12.52) | 12.39 (11.73–21.89) | 14.48 (12.91–33.82) | 0 | 0 | 89.2 (88.9–91.7) | 24.3 (21.2–32.5) |
| 500 | 20,049 (13,145–24,942) | 21.36 (17.43–27.92) | 48.50 (36.44–95.94) | 65.80 (47.45–160.43) | 0 | 0 | 196.3 (194.1–210.8) | 46.6 (43.5–48.5) |
| 1,000 | 19,776 (19,083–21,330) | 35.91 (31.59–38.01) | 105.38 (103.28–110.62) | 200.28 (145.75–212.86) | 0 | 0 | 370.1 (337.4–371.7) | 42.4 (38.7–45.0) |
| 5,000 | 15,663 (6,232–19,660) | 45.35 (20.05–56.89) | 124.26 (42.21–248.51) | 291.50 (63.70–633.34) | 0 | 3,767–4,374 per run | 955.9 (867.1–979.3) | 44.2 (31.2–45.4) |

At 5,000 VUs, some VUs' connects ran out their timeout ("every attempt
failed until the connect timeout"), and the script retries them on the
next iteration. These are the script errors in the raw data. No call on
an established connection failed. Throughput varied from 6,232 to 19,660
calls/s between runs, depending on how quickly the connections were
established.

### GraphQL (`benchmarks/protocol.ps1`, `benchmarks/loadtool/graphql.ts`, demo `/graphql`, 10 ms delay)

| VUs | Operations/s | p50 ms | p95 ms | p99 ms | Failed | Peak private MB | LoadTool CPU % | Server CPU % |
|---|---|---|---|---|---|---|---|---|
| 100 | 8,067 (7,795–8,357) | 11.73 (11.47–11.86) | 15.93 (14.61–18.74) | 19.53 (17.17–27.39) | 0 % | 73.0 (72.8–73.5) | 17.7 (15.2–18.0) | 42.3 (39.6–43.4) |
| 500 | 11,756 (11,348–12,293) | 31.59 (30.28–33.82) | 102.24 (98.04–106.43) | 149.95 (143.65–154.14) | 0.25 % (0.19–0.36) | 127.3 (125.2–128.2) | 24.2 (22.5–25.1) | 54.1 (53.9–55.1) |
| 1,000 | 11,267 (11,229–12,675) | 64.75 (53.74–65.27) | 206.57 (202.38–225.44) | 320.86 (308.28–346.03) | 0.71 % (0.52–0.72) | 202.0 (198.4–202.5) | 24.6 (22.2–25.3) | 52.3 (52.2–56.2) |

The limit here is the demo server's GraphQL execution (graphql-go). It
used twice LoadTool's CPU and levels off near 11,500 operations/s. The
failures are refused connections, as for HTTP.

### Kafka (against kfake, in process)

Kafka results are given in messages, MB and latency, and are not
comparable with HTTP requests per second: a produce is acknowledged by a
broker, not answered by a server, and consumption is a separate stream.

**Produce throughput:** a closed loop with 1 KiB messages, one producer
per VU, waiting for each acknowledgement (`benchmarks/loadtool/kafka-produce.ts`,
`VALUE_BYTES=1024`).

| VUs | Messages/s | MB/s | Producer latency p50 ms | p95 ms | p99 ms | Failed | Peak private MB | CPU % |
|---|---|---|---|---|---|---|---|---|
| 10 | 29,633 (26,148–30,715) | 28.9 (25.5–30.0) | <0.6 (sub-tick) | 0.68 (0.66–0.74) | 1.12 (1.12–1.32) | 0 | 149.7 (131.7–151.7) | 44.9 (37.6–45.8) |
| 100 | 41,516 (35,151–41,792) | 40.5 (34.3–40.8) | 2.11 (2.11–2.38) | 4.95 (4.88–6.19) | 7.57 (7.44–10.29) | 0 | 230.9 (219.7–271.6) | 55.2 (46.9–55.8) |
| 1,000 | 35,078 (33,637–35,174) | 34.3 (32.9–34.4) | 22.68 (21.63–23.72) | 44.83 (43.25–46.40) | 73.92 (69.73–74.97) | 0 | 854.0 (826.5–873.5) | 50.1 (47.5–50.2) |

At 1,000 VUs the slowest produce took 15.8 s in every run: the first
produces, while 1,000 clients connect at once (as on 2026-10-09). The
cost of one client per VU shows in the memory column (ADR-022).

**Pipeline:** producers at a fixed rate (constant arrival rate), and three
consumers in one group (one per partition), with 1 KiB messages
(`benchmarks/loadtool/kafka-pipeline.ts`). Rates are over the 30 s of
sending. Consumer latency is end to end: from a message's production to
its consumption.

| Target rate | Sent/s | MB/s | Consumed | Producer latency p95 ms | Consumer latency p50 ms | p95 ms | p99 ms | Dropped starts |
|---|---|---|---|---|---|---|---|---|
| 1,000/s | 1,000 | 0.98 | all | <0.6 (sub-tick) | 0.60 (0.49–0.67) | 0.98 (0.96–1.14) | 1.34 (1.11–1.53) | 0–6 |
| 5,000/s | 4,998 (4,990–4,999) | 4.88 | all | <0.6 (0.00–0.73) | 0.54 (0.51–0.59) | 1.00 (0.96–1.63) | 1.52 (1.27–6.06) | 27–312 |
| 20,000/s | 19,347 (15,383–19,906) | 18.9 | all | 7.70 (2.15–20.84) | 1.63 (1.06–10.55) | 12.12 (3.75–31.06) | 25.03 (7.37–50.07) | 2,820–138,498 |

- **The consumers kept up at every rate:** every message produced was
  consumed, with no failures.
- **20,000/s was not fully sustained.** The scenario had 200 producer VUs
  (`RATE / 100`). When produce latency rose, starts found no free VU and
  were dropped: 0.5 % of the target in the best run, 23 % in the worst.
  More pre-allocated VUs would be needed for that rate on this machine.

## Notes

**Connection storms.** At 500 VUs and above, HTTP, GraphQL, WebSocket
and gRPC all see refused connections while many connections open at
once. This is one laptop accepting thousands of connections in a burst,
not failed requests on established connections. It was also seen in
Phase 0 and Phase 1 (review item M6).

**The binary grew** from 29 MB (Phase 1) to 46 MB, with the protocol
libraries. This is disk size; the memory figures above are what the
process used.

## 3. Exit criterion 3: retained memory per VU

`BenchmarkVURetainedMemory` ran five rounds alternating the Phase 1 and
Phase 2 test binaries (`raw/vu-retained-memory.txt`). Bytes retained per
VU after its first iteration, medians of five:

| Script | Phase 1 | Phase 2 | Phase 2, all four modules registered |
|---|---|---|---|
| No imports | 4,768 | 4,821 | |
| HTTP only | 5,048 | 5,096 | 5,096 |
| HTTP + sleep | 6,024 | 6,072 | |
| HTTP + setup/teardown | 5,048 | 5,096 | |

Run-to-run variation is at most 15 B.

- **Unused modules cost nothing.** With all four protocol modules
  registered, as the runner registers them, an HTTP-only VU retains
  exactly what it does without them (5,096 B). Before commit `7176dd6`
  they added about 300 B per VU.
- **Every Phase 2 VU retains 48 B more than Phase 1's,** whatever the
  script imports. This is outside run-to-run variation, so **the
  criterion is not met as worded**. The cause: the `VU` struct grew from
  136 to 192 bytes, because Phase 2 added three fields (module list,
  module instances, closers). That moved it from Go's 144-byte allocation
  size class to the 192-byte one. That is 48 KB at 1,000 VUs.
