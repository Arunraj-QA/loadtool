# Benchmarks

Performance claims in this project must link to a result in
[`results/`](results/).

```text
benchmarks/
├── server/    Deterministic target server (Go standard library only)
├── loadtool/  LoadTool scenario and peak-memory measurement script
├── k6/        The same scenario for k6
├── jmeter/    The same scenario for JMeter
└── results/   Recorded results, one file per run: YYYY-MM-DD-topic.md
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
     [loadtool/peak-memory.ps1](loadtool/peak-memory.ps1) does this for
     LoadTool on Windows; k6 and JMeter need the same sampling of their
     process.
   - **CPU use** of the tool's process.
   - Throughput, error rate, and p50 / p90 / p95 / p99 latency.
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

## Known limits

- The JMeter plan was written by hand and has not yet been run with
  JMeter, because JMeter is not installed on the development machine.
- LoadTool scripts cannot read environment variables yet, so the LoadTool
  target is edited in the scenario file.
- Latency timing on Windows has about 0.5 ms resolution (see
  [results/2026-09-24-microbenchmarks.md](results/2026-09-24-microbenchmarks.md)).
