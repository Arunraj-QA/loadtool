# YYYY-MM-DD — <topic>

Copy this file to `YYYY-MM-DD-topic.md` and fill in every field. Write
"not measured" rather than leaving a field out.

## Environment

### Load machine (runs the tool)

| Item | Value |
|---|---|
| CPU (model, cores / threads) | |
| RAM | |
| OS and version | |
| Power / isolation | e.g. plugged in, other apps closed |

### Target machine (runs `benchmarks/server`)

| Item | Value |
|---|---|
| CPU, RAM, OS | |
| Server command | e.g. `./bin/benchserver -addr 0.0.0.0:8080 -delay 10ms` |
| Server commit | `git rev-parse --short HEAD` |
| Network between machines | e.g. 1 Gbit wired, same switch |

### Tool versions

| Tool | Version | Settings changed from default |
|---|---|---|
| LoadTool | commit | |
| k6 | `k6 version` | |
| JMeter | `jmeter --version` | heap: |
| Java | `java -version` | |
| Go (to build LoadTool and server) | `go version` | |

## Scenario

- Files: `benchmarks/loadtool/scenario.ts`, `benchmarks/k6/scenario.js`,
  `benchmarks/jmeter/scenario.jmx` at commit:
- VUs:
- Duration:
- Warm-up: one run per tool of … s, discarded.
- Run order: e.g. LoadTool, k6, JMeter × 3

## Results

One row per run; do not drop runs.

| Tool | Run | Requests | req/s | Error % | p50 | p90 | p95 | p99 | Peak memory | CPU |
|---|---|---|---|---|---|---|---|---|---|---|
| LoadTool | 1 | | | | | | | | | |
| k6 | 1 | | | | | | | | | |
| JMeter | 1 | | | | | | | | | |

How peak memory was measured:

How CPU was measured:

## Summary

Median and range per tool:

## Observations

Anything that may have affected the numbers: errors, background load,
thermal throttling, and so on.
