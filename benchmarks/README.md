# Benchmarks

Performance claims in this project must link to a result in
[`results/`](results/).

```text
benchmarks/
├── measure.ps1    Phase 0 harness: runs all tools at each VU level and records the metrics
├── summarize.ps1  Median and range per tool and VU level from a runs.jsonl
├── server/        Deterministic target server (Go standard library only)
├── loadtool/      LoadTool scenario
├── k6/            The same scenario for k6
├── jmeter/        The same scenario for JMeter
└── results/       Reports (YYYY-MM-DD-topic.md or YYYY-MM-DD-topic/) with raw data
```

## Purpose

These benchmarks compare LoadTool, k6 and JMeter under identical
conditions. The main question for Phase 0: can LoadTool run a scripted HTTP
test at 1,000 VUs using less memory than JMeter at the same VU count?

## The target

All tools run against [`server/`](server/), a deterministic JSON endpoint
with a fixed delay. It is independent of LoadTool and not tuned for any
client. See [server/README.md](server/README.md) for how to start it and
exactly what it returns.

## The scenario

The three scenario files describe the same test and must be changed
together:

| | LoadTool | k6 | JMeter |
|---|---|---|---|
| File | [scenario.ts](loadtool/scenario.ts) | [scenario.js](k6/scenario.js) | [scenario.jmx](jmeter/scenario.jmx) |
| Request | `GET /api/test` | `GET /api/test` | `GET /api/test` |
| Success | status 200, else a script error | `check` status 200 | response assertion 200 |
| VUs / threads | `--vus` | `--vus` | `-Jvus` |
| Duration | `--duration` | `--duration` | `-Jduration` (seconds) |
| Target host | edit `TARGET` in the file | `-e TARGET=…` | `-Jhost=…` `-Jport=…` |

In all three:
- All VUs start at once, with no ramp-up.
- Each VU loops with no think time.
- Keep-alive is on and redirects are not followed.
- Response bodies are not kept for the script: k6 uses
  `discardResponseBodies`, and JMeter stores only an MD5 hash of each
  response.

`127.0.0.1` is used instead of `localhost`: `localhost` resolves to IPv6
first on Windows, and failed IPv6 dials inflate error counts.

## Environment

- **Two machines for comparison runs.** Run the target server on one
  machine and the load tool on another, connected by a wired network.
  - This keeps the server's CPU use out of the tool's measurements.
  - It also avoids the Windows accept-backlog limit: a local Windows
    server refuses connections when about 1,000 clients connect at the
    same instant (see
    [results/2026-09-24-script-execution.md](results/2026-09-24-script-execution.md)).
  - Linux is recommended for the server machine.
- **Same load machine for every tool,** plugged in to power, with other
  applications closed.
- **Pinned versions.** Record the exact version of each tool, and of Java
  for JMeter. Do not change them within one comparison.
- **Default settings,** unless a setting is recorded in the result:
  - JMeter: default heap (`HEAP` env var)
  - k6 and LoadTool: default `GOGC` and `GOMEMLIMIT`
- **Record the environment in every result** using
  [results/TEMPLATE.md](results/TEMPLATE.md).

## Methodology

1. Start the target server and confirm `GET /health` returns 200.
2. **Warm up:** run each tool once for 30 s at the planned VU count and
   discard the result. This warms the JVM, OS caches and connection
   handling.
3. **Measure:** run each tool 3 times for 60 s at the same VU count,
   alternating tools (LoadTool, k6, JMeter, LoadTool, …). Alternating
   spreads machine drift across all tools.
4. For every run, record the tool's own summary and:
   - **Peak memory** of the tool's process: private bytes on Windows, max
     RSS on Linux. For JMeter, measure the `java` process.
   - **CPU use** of the tool's process.
   - Throughput, error rate, and p50 / p90 / p95 / p99 latency.

   [`measure.ps1`](measure.ps1) automates steps 1 to 4 on Windows (see
   below).
5. Report every run, not just the best one. Give the median and range.
6. Check the error rate. Runs with target errors (refused connections,
   timeouts) measure the environment, not the tool; fix the environment
   and rerun.

Commands, with the server at `<host>`:

```bash
go build -o bin/loadtool ./cmd/loadtool
./bin/loadtool run benchmarks/loadtool/scenario.ts --vus 1000 --duration 60s

k6 run -e TARGET=http://<host>:8080/api/test --vus 1000 --duration 60s benchmarks/k6/scenario.js

jmeter -n -t benchmarks/jmeter/scenario.jmx -Jhost=<host> -Jvus=1000 -Jduration=60 -l jmeter.jtl
```

## measure.ps1 (the Phase 0 harness)

Runs the methodology on Windows for LoadTool, k6 and JMeter.

```powershell
# from the repository root, in PowerShell
go build -o bin/loadtool.exe ./cmd/loadtool
go build -o bin/benchserver.exe ./benchmarks/server
$env:JMETER_HOME = "$env:LOCALAPPDATA\loadtool-bench-tools\apache-jmeter-5.6.3"
./benchmarks/measure.ps1                     # 100, 250, 500, 750, 1000 VUs; 60 s; 3 runs; 30 s warm-up
./benchmarks/measure.ps1 -Tools loadtool,k6 -VUs 100,1000 -OutDir benchmarks/results/<name>/raw
```

Run it from a PowerShell prompt or through `powershell -Command "& ./benchmarks/measure.ps1 ..."`.
With `powershell -File`, list values such as `loadtool,k6` arrive as one
string and are rejected.

What it does:
1. Records the environment (CPU, RAM, OS, power, tool versions, commit)
   in `environment.json`.
2. Starts `bin/benchserver.exe` and waits for `/health`.
3. For each VU level, runs a discarded warm-up per tool, then the measured
   runs, alternating tools. It pauses `-CooldownSec` (5 s) between runs.
4. Stops the server.

It refuses to write into an output folder that already has `runs.jsonl`,
so data from different sessions is never mixed.

### Metrics

| Metric | Type | How it is obtained |
|---|---|---|
| Process CPU time, wall time | measured | The tool's process (`java.exe` for JMeter): `TotalProcessorTime`, and `ExitTime − StartTime`. |
| CPU % | calculated | CPU time ÷ wall time ÷ logical CPUs: share of the whole machine. `cpuCores` is the same figure in cores. Covers the whole process lifetime, including start-up. |
| Peak memory | measured | Highest private bytes over samples taken every 250 ms (kept per run in `*-memory.csv`). |
| Average memory | calculated | Mean of those samples, over the whole process lifetime. |
| Requests, errors | measured | LoadTool: console summary. k6: `--summary-export` (`http_reqs.count`, `http_req_failed.passes`). JMeter: counted from the JTL (`success` column). |
| Requests/sec | measured (LoadTool, k6) / calculated (JMeter) | LoadTool and k6 report their own rate. JMeter: requests ÷ time from the first sample start to the last sample end in the JTL. |
| p50, p95, p99 | measured (LoadTool, k6) / calculated (JMeter) | LoadTool and k6 report their own percentiles. Since ADR-004, LoadTool's are histogram values within ±0.78 % of exact. JMeter: nearest rank over the JTL `elapsed` column (whole milliseconds). |
| Error rate | calculated | errors ÷ requests × 100. |
| Server CPU % | calculated | CPU time used by the benchmark server during the run ÷ wall time ÷ logical CPUs. |

Percentile methods differ: LoadTool and the JMeter calculation use
nearest rank, and k6 interpolates. JMeter records whole milliseconds,
while LoadTool and k6 report fractions of a millisecond.

### How each tool is launched

- **LoadTool:** `bin/loadtool.exe run <copy of scenario.ts with TARGET set>`.
- **k6:** the real `k6.exe`, not a Chocolatey launcher, with
  `-e TARGET=…`.
- **JMeter:** `bin\jmeter.bat -n`, so JMeter's default JVM settings
  apply (including its default heap). The measured process is its
  `java.exe` child.
  - The JTL is reduced to `timeStamp`, `elapsed`, `success` and
    `responseCode`, using `-Jjmeter.save.saveservice.*=false`. This
    lowers JMeter's disk work, which favours JMeter.

### Output

All written to `-OutDir`:
- `runs.jsonl`: one JSON line per run, warm-ups included (`kind`).
  Missing values are empty, never estimated.
- `environment.json`
- per run: the tool's stdout and stderr, `*-memory.csv` samples, the k6
  summary JSON, and the JMeter log
- JMeter JTLs, which can be tens of MB, go to `-BulkDir`
  (`bench-out/jtl`, git-ignored). Their size and SHA-256 are recorded in
  `runs.jsonl`.

Then generate the median (min–max) tables:

```powershell
./benchmarks/summarize.ps1 -Runs <OutDir>/runs.jsonl   # writes summary.csv and summary.md
```

## Known limits

- JMeter must be stock 5.6.3 with no third-party plugins in `lib/ext`.
  Plugins add start-up memory and CPU (see
  [results/2026-10-01-phase0-all-tools](results/2026-10-01-phase0-all-tools/README.md)).
- LoadTool scripts cannot read environment variables yet, so the LoadTool
  target is edited in the scenario file.
- Latency timing on Windows has about 0.5 ms resolution (see
  [results/2026-09-24-microbenchmarks.md](results/2026-09-24-microbenchmarks.md)).
