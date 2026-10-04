# 2026-10-04 — Phase 1 step 4 (DSL core): per-VU memory

Step 4 added built-in modules, relative imports, `__ENV`/`__VU`/`__ITER`,
`console`, `sleep` and `group` (ADR-007). Each of these adds objects to
every VU's runtime. This record checks the step's memory gate: retained
memory per VU should stay at about 6 KB, the level before step 4.

## Environment

### Load machine (also runs the target)

| Item | Value |
|---|---|
| CPU (model, cores / threads) | 12th Gen Intel Core i5-1235U, 10 cores / 12 threads |
| RAM | 16 GB (16,069 MB) |
| OS and version | Windows 11 Enterprise 10.0.26200 |
| Power / isolation | not recorded; normal desktop session |

### Target

| Item | Value |
|---|---|
| CPU, RAM, OS | same machine as the load generator |
| Server command | `benchserver -addr 127.0.0.1:8080 -delay 10ms` (`benchmarks/server`) |
| Server commit | step 4 working tree (the server is unchanged since Phase 0) |
| Network between machines | loopback |

### Tool versions

| Tool | Version | Settings changed from default |
|---|---|---|
| LoadTool "before" | commit `bbbb685` (end of step 3) | none |
| LoadTool "after" | step 4 branch `p1/dsl-core`, working tree with the lazy built-ins and IIFE bundle format | none |
| Go | go1.27.0 windows/amd64 | |

The "after" binary was built just before a final edit that replaced
`slices.IndexFunc` with a loop in `lazyObject.index`. That edit changes
the cost of a property lookup, not what is allocated at VU start-up.

## 1. Go benchmark: retained heap per VU

`BenchmarkVURetainedMemory` creates 1,000 VUs and reports the live heap
they keep after `runtime.GC()`, divided by 1,000. It excludes goroutine
stacks and HTTP connections. Results are deterministic (identical across
`-count=3`).

```bash
go test -run '^$' -bench VURetainedMemory -benchtime=5x -count=3 ./internal/script/
```

| Script | `bbbb685` | Step 4, first version (eager) | Step 4, final |
|---|---|---|---|
| No imports | 5,984 B | 12,984 B | 4,608 B |
| Imports `loadtool/http` | 5,984 B ¹ | not measured | 6,568 B |
| Imports `loadtool/http` and `loadtool` | n/a ² | not measured | 7,704 B |

1. At `bbbb685` every VU built the `http` global whether or not the script
   used it, so the "no imports" figure is also the http figure.
2. The `loadtool` module did not exist.

After the first iteration the figures are at most 48 B higher.

Other script benchmarks at the final version, with `bbbb685` in brackets:

- `BenchmarkNewVU`: 7,872 B/op, 78 allocs/op (6,288 B, 57 allocs).
- `BenchmarkIterateEmpty`: 230 B/op, 4 allocs/op (230 B, 4 allocs).

### What was changed to pass the gate

The first version of step 4 measured 12,984 B/VU, which fails the gate.

**Diagnosis.** Stubbing out each built-in in turn (measured, not
committed) showed where the memory went:

| Built-in removed | Saved per VU |
|---|---|
| The 5 console methods | 3,726 B |
| `sleep` | 941 B |
| The http module | 1,589 B |

**Measurement.** A function object costs about 750 B per goja runtime.
This held for both kinds of function:

| Five functions per runtime, built as | Cost per runtime |
|---|---|
| Native (Go) | 6,984 B |
| JavaScript | 7,272 B |
| JavaScript, each calling one shared native function | 8,320 B |
| Empty runtime, for comparison | 3,400 B |

So routing calls through one Go function does not help.

**Fixes:**

1. Built-in modules and console methods are built on first access (a
   `lazyObject`).
2. Bundles use esbuild's IIFE format. Top-level declarations stay scoped
   to the script, as in an ES module, instead of becoming properties of the
   global object. This saved another 1.3–2.1 KB per VU.

## 2. Process level: 1,000 VUs, before vs after

**Method:**

- `ab.ps1` in this folder.
- 1,000 VUs for 20 s per run, 3 rounds per scenario.
- The order alternates each round: before → after, then after → before.
- 3 s pause between runs.
- Peak working set and peak private bytes are the highest values of
  `WorkingSet64` and `PrivateMemorySize64`, sampled every 250 ms.

**Scenarios:**

- **http:** `GET /api/test` per iteration, like
  `benchmarks/loadtool/scenario.ts`.
  - `http-before.ts` uses the `http` global, which is all `bbbb685`
    supports.
  - `http-after.ts` imports `loadtool/http`.
- **cpu:** no HTTP; each iteration sums 0..999 (`cpu.ts`). This isolates
  the script runtime.

Raw rows are in `runs.csv`; no runs were dropped.

### Measured

| Scenario | Variant | Peak private MB (median, range) | Peak working set MB (median, range) | req/s (median, range) | Errors |
|---|---|---|---|---|---|
| http | before | 197.6 (177.7–198.3) | 148.1 (134.2–149.2) | 33,232 (31,073–36,816) | 0.26–1.50 % |
| http | after | 190.8 (188.7–193.7) | 144.1 (140.9–147.2) | 34,949 (28,150–40,404) | 0.22–1.61 % |
| cpu | before | 142.7 (142.5–178.2) | 159.1 (143.4–159.6) | — | — |
| cpu | after | 139.7 (129.6–146.1) | 111.2 (93.9–156.4) | — | — |

### Observations

- **No process-level memory difference was detected at 1,000 VUs.** The
  before and after ranges overlap in both scenarios.
  - The heap benchmark predicts +0.6 MB for the http scenario (584 B × 1,000
    VUs). That is well inside the run-to-run spread of about 20 MB.
  - These runs do not show that step 4 uses less memory, either.
- **The cpu scenario allocates heavily**, about 14 KB per iteration because
  of goja integer boxing. Its peaks depend on when the GC runs, which
  explains the wide ranges.
  - A 10-VU check of the same scenario (4 rounds, not recorded here) gave
    overlapping ranges too: 58–115 MB before, 86–136 MB after.
- **Errors at 1,000 VUs with the server on the same laptop** are the open
  review item M6. They appear in both variants at similar rates.
- **req/s varies by ±15% between runs** in both variants. These runs are
  not a throughput comparison.

## Gate result

**Gate:** retained memory per VU stays at about 6 KB.

- For a script that uses http, it is 6,568 B against 5,984 B before step 4:
  +584 B, +10%.
- Scripts that also import `loadtool` (for `sleep`) cost 7,704 B.
- No difference was visible at process level at 1,000 VUs.

The gate is treated as met for the http case. The `sleep` cost is
recorded here so later steps can be compared against it.
