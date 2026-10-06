# 2026-10-06 — Phase 1 step 7b (scenario engine): constant-VU path A/B

Step 7b rebuilt the engine around scenarios and executors. The Phase 0
load model (`--vus`/`--duration`) now runs as one `constant-vus`
scenario through `engine.RunScenarios`. This record checks that path did
not get worse.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 16 GB, Windows 11 Enterprise 10.0.26300 |
| Target | `benchserver -addr 127.0.0.1:8080 -delay 10ms`, same machine (loopback) |
| Before | commit `a6de169` (`phase-1-mvp` after step 6) |
| After | branch `p1/scenarios`, working tree at the end of step 7b (engine, config, script and runner changes) |
| Go | go1.27.0 windows/amd64 |

## Method

- Script: `../2026-10-04-dsl-core-memory/ab.ps1`, run with `-Before`, `-After`
  and `-Server`.
- Runs: 1,000 VUs, 20 s per run, 3 rounds, alternating order each round,
  with 3 s between runs.
- Peaks: highest `WorkingSet64` / `PrivateMemorySize64`, sampled every
  250 ms.
- **http scenario:** `benchmarks/loadtool/scenario.ts` with
  `discardResponseBodies: true`, the same file for both binaries.
- **cpu scenario:** no HTTP; each iteration sums 0..999 (the same `cpu.ts`
  as the 2026-10-04 record).
- Raw rows are in `runs.csv`; none were dropped.

## Measured

| Scenario | Variant | Peak private MB (median, range) | Peak working set MB (median, range) | req/s (median, range) | Errors |
|---|---|---|---|---|---|
| http | before | 168.7 (166.4–174.1) | 131.3 (127.9–135.0) | 40,295 (36,457–41,040) | 1,390–2,074 |
| http | after | 170.5 (165.3–179.4) | 131.4 (129.1–137.5) | 36,791 (35,961–41,339) | 1,031–1,271 |
| cpu | before | 122.5 (113.0–129.7) | 87.5 (77.5–94.2) | — | — |
| cpu | after | 120.2 (116.2–125.9) | 85.2 (81.0–91.3) | — | — |

## Observations

- **No difference was detected** in memory or throughput. Every before
  range overlaps its after range. These runs show neither a regression
  nor an improvement.
- **Throughput.** req/s varies by about ±7% between runs of the same
  binary; the medians differ in opposite directions in different rounds.
- **Errors** at 1,000 VUs with the server on the same laptop are the open
  review item M6, present in both variants.
- **Retained heap per VU**, from `BenchmarkVURetainedMemory` (no imports):
  4,640 B before, 4,661 B after. The 21 B are the VU's new `exec` field.
