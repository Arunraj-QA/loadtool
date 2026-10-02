# LoadTool architecture (Phase 0)

This describes the code as it stands at the end of Phase 0 (October 2026).
- **Decisions:** the *why* behind individual choices is in the
  [architecture decision records](decisions/README.md).
- **Numbers:** measured figures come from [`benchmarks/results`](../benchmarks/results/)
  and are dated, because they were taken at different commits.

## 1. What LoadTool is

LoadTool is a load-testing engine written in Go.
- Users write a test as a **TypeScript or JavaScript file** that exports a
  default function.
- LoadTool runs that function in a loop on **N virtual users (VUs)**, one
  goroutine each, for a fixed duration.
- Each VU sends **HTTP/1.1** requests to the target and records them.
- At the end, LoadTool prints a summary: request and error counts,
  throughput, and latency percentiles.

```bash
loadtool run examples/basic-http.ts --vus 1000 --duration 60s
```

Phase 0 is deliberately small: one process, one protocol, one load model.
Distributed execution, storage, dashboards and other protocols belong to
later phases (see `CLAUDE.md`).

## 2. System context

```mermaid
flowchart LR
    user([User]) -->|"test.ts, --vus, --duration"| lt["loadtool<br/>(one process)"]
    lt -->|"HTTP/1.1 requests"| target[(Target API)]
    lt -->|"console summary"| user
    subgraph bench ["benchmarks/ (separate from the product)"]
        harness["measure.ps1"] --> lt
        harness --> k6[k6] & jm[JMeter]
        lt & k6 & jm --> bserver["benchserver<br/>(deterministic target)"]
    end
```

The benchmark tooling uses LoadTool from the outside, like any other tool,
and the benchmark server is independent of LoadTool's code (§11).

## 3. Components and dependencies

```mermaid
flowchart TD
    main["cmd/loadtool<br/>entry point, Ctrl+C"] --> cli
    cli["internal/cli<br/>Cobra commands, summary"] --> config["internal/config<br/>run settings"]
    cli --> script["internal/script<br/>TS/JS → goja, one runtime per VU"]
    cli --> engine["internal/engine<br/>goroutine per VU, timing"]
    cli --> httpclient["internal/httpclient<br/>HTTP/1.1 execution"]
    script --> httpclient
    script --> metrics["internal/metrics<br/>histograms, summary"]
    httpclient --> metrics
    engine --> metrics
```

The arrows are the actual import graph (`go list`). Three boundaries
matter:
- **`engine` imports neither `script` nor `httpclient`.** It schedules
  opaque iterations (`IterationFunc`) and knows nothing about JavaScript
  or HTTP. New protocols or script runtimes plug in without changing it.
- **`cli` is the only place that wires components together.** No package
  reaches across to another's internals, and there is no global mutable
  state.
  - The package-level variables that exist are never modified after
    start-up: error values, the script entry text and the computed bucket
    count.
  - The one exception is `cli.Version`, which is set at build time.
- **`metrics` is the shared leaf.** Every layer records into it, and it
  depends on nothing.

| Package | Responsibility | Key API | Lines (code / tests) |
|---|---|---|---|
| `cmd/loadtool` | Process entry; maps Ctrl+C to context cancellation; exit codes | `main` | 26 / 0 |
| `internal/cli` | Flags, validation, wiring, console summary | `NewRootCmd` | 201 / 262 |
| `internal/config` | Run settings and validation | `Config.Validate` | 39 / 55 |
| `internal/engine` | VU start-up, one goroutine per VU, duration and graceful stop | `Run`, `IterationFunc`, `NewVUFunc` | 81 / 258 |
| `internal/script` | Transpile and compile scripts; per-VU goja runtime; the `http` script API | `Load`, `Compile`, `Program.NewVU`, `VU.Iterate` | 364 / 576 |
| `internal/httpclient` | Tuned HTTP/1.1 client; sends one request, times it, records it | `New`, `Do` | 112 / 328 |
| `internal/metrics` | Latency histograms, counters, merging into a summary | `NewRecorders`, `Recorder`, `Merge`, `Summary` | 269 / 308 |
| `benchmarks/server` | Deterministic benchmark target (standard library only) | `GET /api/test`, `GET /health` | 113 / 184 |

External modules: `spf13/cobra` (CLI), `dop251/goja` (JavaScript engine)
and `evanw/esbuild` (TypeScript transpiling). Everything else is the Go
standard library.

## 4. Lifecycle of a run

```mermaid
sequenceDiagram
    autonumber
    participant M as main
    participant C as cli.runTest
    participant S as script
    participant E as engine.Run
    participant V as VU goroutines
    participant H as httpclient
    participant X as metrics

    M->>C: ExecuteContext(ctx)  [Ctrl+C cancels ctx]
    C->>S: Load(test.ts): esbuild bundle → goja.Compile (once)
    C->>H: New(VUs, 30s): one shared http.Client
    C->>E: Run(ctx, vus, duration, gracefulStop, newVU)
    loop for each VU, sequentially, before the clock starts
        E->>S: newVU(i) → Program.NewVU(ctx, client)
        S-->>E: VU.Iterate  (own goja runtime, top-level code run once)
    end
    Note over E: start clock: stopStarting = now + duration<br/>hard deadline = stopStarting + gracefulStop
    E->>X: NewRecorders(vus): 16 shared shards
    par one goroutine per VU
        loop while now < stopStarting and ctx not done
            V->>S: Iterate(ctx, rec): call default()
            S->>H: http.get / http.request → Do(ctx, client, req, rec)
            H->>X: rec.Record(latency, ok)
        end
    end
    E->>E: wait for all VUs (in-flight iterations may finish until the hard deadline)
    E->>X: Merge(recorders) → Summary
    E-->>C: Result{Summary, Elapsed}
    C-->>M: print summary; exit 1 if interrupted
```

**Phases of a run:**
1. **Compile once.** esbuild transpiles and bundles the script, and goja
   compiles it into an immutable `*goja.Program` that all VUs share.
2. **Start every VU before the clock.** Each VU gets its own goja runtime
   and runs the script's top-level code once. Any error here aborts the
   run before any load is sent. VU start-up time is therefore not part of
   the measured duration.
3. **Load phase.** Every VU loops, calling the script's default function,
   until `--duration` ends.
4. **Graceful stop.** After `--duration`, no new iterations start. Running
   ones may finish for up to `--graceful-stop` (default 30 s) and are
   counted; after that they are cancelled.
5. **Merge and report.** The recorders are merged into one `Summary` after
   all VU goroutines have exited.

## 5. The hot path

Per iteration, per VU:

| Step | Where | Notes |
|---|---|---|
| Check the stop time and context | `engine.runVU` | `time.Now()` against the stop time, plus `ctx.Err()` |
| Register the interrupt hook | `script.VU.Iterate` | `context.AfterFunc(ctx, rt.Interrupt)`, so a script stuck in a loop can be stopped |
| Call the default function | goja | The VU's own runtime; no locks |
| `http.get` / `http.request` | `script/http.go` | Converts JS arguments, builds `httpclient.Request` |
| Send and time the request | `httpclient.Do` | Builds the request, `client.Do`, reads the body to the end, closes it |
| Record | `metrics.Recorder.Record` | One atomic increment in a shared histogram shard; 0 allocations |
| Build the JS response | `script/http.go` | `{status, error, timings: {duration}}` |

Measured costs, each as of its date:

| Measure | Value | Source |
|---|---|---|
| Iteration overhead, empty script | ~370 ns, 4 allocations | 2026-09-24 script-execution results |
| Script layer on top of an HTTP request | +17 allocations, ~1.5 KB | same |
| `Record` | 16–24 ns, 0 B, 0 allocations | 2026-10-01 latency-histogram results |
| `Record` from 12 CPUs into shared shards | ~3 ns/op total | same |

The hot path takes **no mutex of LoadTool's own**. It does share two
contention points with every VU:
- **One context.** The per-iteration `AfterFunc` and `http.Client`'s
  per-request deadline both register on the run's single context, whose
  mutex all VUs share. Measured: about 0.8M iterations/s maximum on 12
  CPUs. That is about 2–3 % busy at the 20–25k req/s seen so far.
- **The `http.Transport` connection pool,** which has its own locking.

## 6. Concurrency model

### Goroutines

| Goroutine | Count | Lifetime |
|---|---|---|
| main / Cobra | 1 | Process |
| VU | one per VU | From the clock starting until the VU's loop ends; `Run` waits for all of them (`sync.WaitGroup`) |
| `net/http` connection goroutines | 2 per open connection (read and write loops) | Until the connection closes; `CloseIdleConnections` runs at the end of `runTest` |
| `AfterFunc` callbacks | short-lived | Only when a context ends; they call `rt.Interrupt` |

No hidden long-lived goroutines are started. The `-race` tests in CI and
goroutine-count probes (counts back to baseline after completed,
interrupted and endless-loop runs) cover leaks and races.

### Shared and per-VU state

| State | Shared by | Why it is safe |
|---|---|---|
| `*goja.Program` | all VUs | Immutable; goja documents it as safe for concurrent use |
| `*http.Client` / `Transport` | all VUs | Safe for concurrent use; one connection pool |
| Histogram shards (16) | about VUs/16 each | Atomic counters, and CAS for min and max |
| `goja.Runtime` | **one VU** | Not goroutine-safe; only its VU's goroutine touches it, apart from `Interrupt`, which goja documents as safe |
| `VU.ctx`, `VU.rec` | one VU | Set and read on the VU's goroutine during `Iterate` |
| `Recorder` counters (unsent, script errors) | one VU | Plain ints, read only after `wg.Wait()` |

### Contexts and stopping

```mermaid
flowchart LR
    sig["signal.NotifyContext<br/>(Ctrl+C)"] --> run["run context"]
    run --> dl["hard deadline<br/>= stopStarting + gracefulStop"]
    dl --> req["per-request deadline<br/>(http.Client.Timeout 30s)"]
    dl -.->|"AfterFunc → rt.Interrupt"| js["JS execution"]
```

| Event | Effect |
|---|---|
| `--duration` ends | VUs stop starting iterations; running ones continue |
| Graceful stop ends | Context cancelled: requests aborted, JS interrupted; nothing in flight is recorded |
| First Ctrl+C | Context cancelled at once (VU start-up included); a partial summary is printed; exit code 1 |
| Second Ctrl+C | Default signal handling is restored after the first, so the process terminates |
| Request timeout (30 s) | The request fails and is counted as an error |

## 7. Script runtime

```mermaid
flowchart LR
    ts["test.ts / test.js"] --> plugin["esbuild plugin<br/>serves the source from memory,<br/>rejects other imports"]
    entry["generated entry<br/>import fn from 'loadtool:script'<br/>globalThis.__loadtool_default = fn"] --> esb
    plugin --> esb["esbuild Build<br/>bundle, ES2017, strip types"]
    esb --> sm["inline source map<br/>(file names cleaned)"]
    sm --> goja["goja.Compile → *goja.Program"]
```

- **Bundling, not CommonJS conversion.** Binding the default export inside
  one bundle produces no interop helpers. That cut each VU's retained
  memory from about 30 KB to about 6 KB (ADR-001).
- **Isolation.** Each VU has its own runtime, so module-level variables
  are per VU.
- **API.**
  - `http.get(url, params?)` and `http.request(method, url, body?, params?)`
    return `{status, error, timings: {duration}}`.
  - Transport errors don't throw; they set `status: 0` and `error`.
  - HTTP calls in top-level code throw.
- **Limits.**
  - Call depth is 2,500 frames per VU. Runaway recursion becomes a script
    error; worst case is about 1.7 MB per VU.
  - Scripts can't import other files.
  - TypeScript types are stripped, not checked.
  - No `async`.
- **Errors.** An exception ends that iteration only. It's counted, and the
  first message (with the `.ts` line, via the source map) appears in the
  summary.

## 8. HTTP layer

`httpclient.New(VUs, 30s)` builds one client:

| Setting | Value | Reason |
|---|---|---|
| `MaxConnsPerHost` | VUs | At most one connection per VU. Without it, background dials piled up and a 1,000-VU run crashed with thread exhaustion on Windows. |
| `MaxIdleConns(PerHost)` | VUs | Every VU keeps its keep-alive connection |
| HTTP/2 | disabled, including over TLS | Phase 0 is HTTP/1.1 only |
| Redirects | not followed | One iteration = one measured request |
| `DisableCompression` | true | No implicit `Accept-Encoding: gzip`, matching k6 and JMeter |
| Timeout | 30 s, including reading the body | — |

`Do` times from just before `client.Do` until the body is fully read and
closed, so latency includes any connection setup. A 2xx or 3xx status
counts as success.

## 9. Metrics

- **Storage.** Log-linear histograms: 64 sub-buckets per power of two and
  2,368 buckets up to 1 h, about 19 KB each (ADR-004).
- **Layout.** Each of the **16 shards** holds a successful-request and a
  failed-request histogram. VU *i* records into shard *i* mod 16 with
  atomic operations. Total about **0.6 MB**, independent of VU count,
  duration and request rate.
- **Precision.** Percentiles are within ±0.78 %. Counts, min, max and mean
  are exact.
- **Two latency sets in the summary:** all requests that were sent (the
  population k6 and JMeter use), and successful requests only, which fast
  failures such as refused connections cannot pull down.
- **Requests never sent** (for example invalid URLs) count as failures
  without a latency sample.

## 10. Memory

Measured per VU (retained heap):
- goja runtime: about **6 KB**
  (`BenchmarkVURetainedMemory`, 2026-10-01)
- plus the VU's goroutine stack and its connection's buffers and two
  goroutines (not measured separately)

Measured per process (2026-10-01 comparison, 60 s runs, before the
histogram):

| VUs | Peak private bytes | Average working set |
|---|---|---|
| 100 | 70–77 MB | 33–35 MB |
| 1,000 | 190–210 MB | 119–152 MB |

- At 1,000 VUs most of the peak is GC headroom and short-lived per-request
  garbage, not per-VU state.
- Since ADR-004, peak memory does not grow with run length: 62.0 MB at
  20 s and 61.8 MB at 120 s for the same 50-VU test.

## 11. Benchmark subsystem

Separate from the product, under `benchmarks/`:

| Part | Role |
|---|---|
| `server/` | Deterministic target: fixed JSON body after a fixed delay, no `Date` header, standard library only (a test enforces this) |
| `loadtool/`, `k6/`, `jmeter/` | The same scenario for each tool: `GET /api/test`, all VUs at once, no think time, keep-alive, success = 200 |
| `measure.ps1` | Harness: starts the server, records the environment, runs warm-ups, then alternating measured runs; samples each tool's process for CPU and memory; one JSON line per run |
| `summarize.ps1` | Median and range tables from `runs.jsonl` |
| `results/` | Dated reports with raw data, separating measured values, calculated values and observations |

## 12. Quality gates

- **Tests.** Go `testing`, with every package covered. HTTP tests use
  local `httptest` servers; no test depends on the internet. Key
  behaviours are checked by mutation: the tests fail when the fix is
  removed.
- **CI** (`.github/workflows/ci.yml`):
  - gofmt and `go vet`
  - `go test` on Linux and Windows
  - `go test -race -count=3` on Linux, because the race detector needs cgo,
    which the Windows development machine lacks
- **Benchmarks.** Micro-benchmarks for recording, merging, VU creation
  and the iteration path. End-to-end comparisons through `measure.ps1`.

## 13. Known limitations and open items

From the Phase 0 performance review:

| Item | Status |
|---|---|
| Latency definitions differ between tools (LoadTool includes connection setup and pool wait; k6's `http_req_duration` does not) | Open (M2) |
| LoadTool logs more errors than k6 against the same refusing server | Open, not explained (M6) |
| Comparison runs were on one laptop; the target refused connections at ≥250 VUs | Needs a separate server machine (H3) |
| Shared-context mutex per iteration | Not a bottleneck at measured rates (L1) |
| `MaxIdleConns` is a total across hosts; multi-host scripts would churn connections | Open (L3) |
| A Go panic inside one iteration ends the process (no per-VU recover) | Open (L4) |
| Windows timer resolution about 0.5 ms | Platform limit |

## 14. Extension points for later phases

These are the seams the current design leaves open. None is implemented,
and each would need its own decision record.
- **Other protocols:** a new package that records into
  `metrics.Recorder`, exposed to scripts the way `http` is.
  `engine` does not change.
- **Load models** (ramping, arrival rate): replace or extend the loop in
  `engine.runVU` and the VU start-up in `engine.Run`. The
  `IterationFunc`/`NewVUFunc` contract stays.
- **Distributed execution:** histogram shards merge by adding bucket
  counts, so results from several machines can be combined exactly the
  same way.
- **Result output** (files, dashboards): consume `metrics.Summary`, or
  the shard histograms for full distributions, from `cli`.
