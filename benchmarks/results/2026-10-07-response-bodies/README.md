# 2026-10-07 — Response bodies and memory (ADR-013)

A user reported Phase 1 using far more memory than Phase 0 at 1,000 VUs.
This run measures whether keeping response bodies is the cause, and
whether ADR-013 (discard bodies by default) fixes it.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300; the same laptop as `2026-10-07-phase1` |
| Power / isolation | **On AC power** (`PowerOnline: True`); no other work was started during the runs |
| Target | `benchmarks/server` on the same machine, `GET /api/large` with `-large-size 1048576` (1 MiB JSON body), 10 ms delay |
| Go | go1.27.0 windows/amd64 |

**Builds compared:**

| Label | Build | Script |
|---|---|---|
| `phase0` | Phase 0, `3b7b523` (always discards bodies) | `raw/large-body-phase0.ts` (Phase 0 syntax: global `http`) |
| `phase1-before` | Phase 1 before ADR-013, `a87a2d9` (keeps bodies by default) | `benchmarks/loadtool/large-body.ts` |
| `new-default` | ADR-013, `ba1afed` (discards by default) | `benchmarks/loadtool/large-body.ts` |
| `new-keep` | ADR-013, `ba1afed` | `benchmarks/loadtool/large-body-keep.ts` (`discardResponseBodies: false`) |

No script reads the body; each GETs `/api/large` and checks the status.

## Command

```powershell
./benchmarks/body-memory.ps1 -VUs 1000 -DurationSec 30 -Runs 3 -WarmupSec 10 -OutDir <dir> -Cases @(
  'phase0|bin/loadtool-p0.exe|benchmarks/results/2026-10-07-response-bodies/raw/large-body-phase0.ts',
  'phase1-before|bin/loadtool-p1pre.exe|benchmarks/loadtool/large-body.ts',
  'new-default|bin/loadtool.exe|benchmarks/loadtool/large-body.ts',
  'new-keep|bin/loadtool.exe|benchmarks/loadtool/large-body-keep.ts')
```

**What the harness does:**

1. One 10 s warm-up per case.
2. Three rounds of 30 s runs, with the cases interleaved in each round.
3. Peak private bytes and peak working set, sampled every 200 ms.
4. Requests are read from the summary.

Every run is in `runs.jsonl`.

## Measured

**Session 2** (`raw/session2/`) is the complete, comparable session.
Medians of three runs, with ranges:

| Case | Peak private MB | Peak working set MB | Requests in 30 s |
|---|---|---|---|
| `phase0` | 178.7 (169.6–191.9) | 126.8 (121.8–143.5) | 120,703 (109,608–130,686) |
| `phase1-before` | **1,959.1** (1,806.5–2,039.8) | 1,920.4 (1,757.0–2,001.9) | 72,744 (66,112–73,211) |
| `new-default` | **175.8** (172.0–237.5) | 125.9 (120.7–175.4) | 118,272 (115,540–118,590) |
| `new-keep` | 2,044.3 (2,043.4–2,045.9) | 2,006.8 (2,003.6–2,007.0) | 72,274 (60,159–73,546) |

**Session 1** (`raw/`) ran the same cases earlier.

- The three Phase 1 cases agree with session 2:
  - `phase1-before`: 1,968–2,045 MB;
  - `new-default`: 159–174 MB;
  - `new-keep`: 1,931–2,047 MB.
- **Every `phase0` run failed** (exit 1, `unknown shorthand flag: 'e'`):
  the harness passed `-e BASE_URL=…`, which Phase 0 does not have. The
  harness now passes `BASE_URL` through the environment instead. These
  failed runs are kept in `raw/runs.jsonl`, and session 2 was run to
  measure all four cases together.

## Conclusions

- **Keeping bodies caused the regression.** With 1 MiB responses at
  1,000 VUs:
  - Phase 1 before ADR-013 peaked at about 2 GB of private memory, 11×
    Phase 0;
  - it completed about 40 % fewer requests.
- **ADR-013 restores the Phase 0 behaviour.** The new default's peak
  memory and requests are within the run-to-run range of the Phase 0
  build in the same session. One `new-default` run peaked at 237.5 MB;
  the other two were 172–176 MB.
- **Opting in costs what it did before.** With bodies kept, the new
  build behaves like the old default, about 2 GB.

**Caveats:**

- One laptop runs both LoadTool and the server, so requests per second
  describe this setup, not a capacity.
- The memory peak depends on body size × VUs. 1 MiB was chosen to make
  the effect clear; small responses (as in the Phase 1 exit benchmark)
  show no difference.
