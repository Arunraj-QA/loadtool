# 2026-10-07 — Phase 1 exit benchmark

This run checks the Phase 1 exit criteria, agreed and recorded in
`CLAUDE.md` (commit `4a9aea4`) before it started:

1. At 1,000 VUs, LoadTool's memory is lower than JMeter's, measured as on
   2026-10-01.
2. At 1,000 VUs, LoadTool's peak memory and requests per second are within
   run-to-run variation of the Phase 0 build, measured in the same
   session.
3. Every local example passes in CI and the GitHub Action self-test
   passes.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`onACPower: true`); no other work was started during the runs |
| Target | `benchmarks/server` on the same machine, `GET /api/test`, 10 ms delay |
| LoadTool | commit `4a9aea4` (Phase 1); Phase 0 build `3b7b523` for criterion 2 |
| k6 | v1.7.1 |
| JMeter | 5.6.3, the clean copy without plugins used on 2026-10-01, default JVM settings, OpenJDK 17.0.19 |
| Go | go1.27.0 windows/amd64 |

Everything is captured in `raw/environment.json`.

## 1. LoadTool, k6 and JMeter at 1,000 VUs

**Command.** The same harness and settings as the Phase 0 comparison:

```powershell
./benchmarks/measure.ps1 -Tools loadtool,k6,jmeter -VUs 1000 -DurationSec 60 -Runs 3 -WarmupSec 30 `
  -OutDir benchmarks/results/2026-10-07-phase1/raw -JMeterHome <clean JMeter copy>
```

**Run order.** One 30 s warm-up per tool, then three rounds of 60 s runs
in the order LoadTool, k6, JMeter. Every run is in `raw/runs.jsonl`.

**Scenario.** The same work for each tool, as on 2026-10-01: the
scenarios in `benchmarks/loadtool`, `benchmarks/k6` and
`benchmarks/jmeter`, with bodies discarded.

### Measured

Medians of three runs, with ranges:

| Tool | Peak private MB | Avg private MB | CPU % | req/s | p95 ms | Errors |
|---|---|---|---|---|---|---|
| LoadTool | **189.0** (183.0–195.5) | 177.2 (173.1–185.8) | 40.2 (39.1–41.2) | 40,050 (34,502–40,332) | 44.3 (43.8–60.6) | 0.07–0.10 % |
| k6 | 728.8 (708.6–742.9) | 503.5 (502.0–510.8) | 47.6 (47.3–48.0) | 29,690 (29,218–30,420) | 60.6 (57.0–61.3) | 0.03–0.05 % |
| JMeter | 1,378.8 (1,366.5–1,380.3) | 1,320.8 (1,312.5–1,330.2) | 42.4 (40.9–43.7) | 34,051 (30,849–34,634) | 66 (64–81) | 0.04–0.05 % |

**Notes:**

- **Memory.** LoadTool's peak private memory (183–196 MB) is in the same
  range as Phase 0's 190–210 MB on 2026-10-01.
- **Latency and throughput are not a tool comparison.** One laptop runs
  the tools and the target, and the tools define latency slightly
  differently (Phase 0 review item M2). These numbers describe this setup
  only.
- **Errors** are refused or failed connections at 1,000 VUs on one
  machine, for all three tools (review item M6).

## 2. Phase 0 build against Phase 1 build, same session

**Method.** `../2026-10-04-dsl-core-memory/ab.ps1` ran the Phase 0 build
(`3b7b523`) and the Phase 1 build (`4a9aea4`):

- 1,000 VUs, 60 s per run, 3 rounds;
- the order alternates each round, with 3 s between runs;
- peaks are sampled every 250 ms.

**The same work.** Both scripts make one `GET /api/test` per iteration
and fail on a non-200 status. The Phase 0 build always discards bodies;
the Phase 1 script sets `discardResponseBodies: true`. Raw rows are in
`ab-phase0-vs-phase1.csv`.

| Build | Peak private MB | Peak working set MB | req/s | Errors |
|---|---|---|---|---|
| Phase 0 | 218.9 (213.1–229.2) | 172.5 (172.4–186.6) | 30,326 (18,309–39,920) | 1,141–3,389 |
| Phase 1 | 184.3 (179.3–192.0) | 142.2 (139.3–147.6) | 39,309 (20,938–40,261) | 1,143–3,336 |

**Rounds, side by side:**

| Round | Phase 0 req/s | Phase 1 req/s |
|---|---|---|
| 1 | 39,920 | 39,309 |
| 2 | 30,326 | 40,261 |
| 3 | 18,309 | 20,938 |

**Notes:**

- **Memory.** Phase 1's peak private memory was lower than Phase 0's in
  every round, by about 30–40 MB. The criterion only asks for no
  regression; the cause has not been investigated.
- **Throughput.** It varies widely between rounds for *both* builds:
  round 3 was about half of round 1 for both, so the machine, not the
  build, set the pace. Within each round the two builds are close, and
  the ranges overlap.

## Verdict

| Criterion | Result | Evidence |
|---|---|---|
| 1. Memory below JMeter at 1,000 VUs | **Met** | LoadTool 183–196 MB vs JMeter 1,367–1,380 MB peak private, every run (section 1) |
| 2. No regression against Phase 0 | **Met** | Memory lower in every round; requests per second within run-to-run variation (section 2) |
| 3. Features end to end in CI | **Met** | The CI job "release-and-action" (examples smoke test and GitHub Action self-test) passed on `3798304`, the last `phase-1-mvp` commit before this run, and on this branch (see the commit that adds this file) |

**Caveats** (as in Phase 0):

- **One machine** ran the tools and the target, so absolute throughput
  and latency are limited by that machine.
- **JMeter** used its default JVM settings.
