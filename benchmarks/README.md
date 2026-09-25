# Benchmarks

Performance claims in this project must link to a result in
[`results/`](results/).

```text
benchmarks/
├── server/    Shared HTTP target server (Go, standard library only)
├── loadtool/  LoadTool scenario and peak-memory measurement script
├── k6/        The same scenario for k6
├── jmeter/    The same scenario for JMeter
└── results/   Recorded results, one file per run: YYYY-MM-DD-topic.md
```

## The comparison scenario

The three scenario files describe the same test, so they must be changed
together:

- `GET http://127.0.0.1:8080/` against `server/`
- N VUs (threads in JMeter), all started at once, no ramp-up
- Each VU loops with no think time until the duration ends
- A request succeeds only with status 200
- Keep-alive on, redirects not followed, response bodies not kept for the
  script (k6 `discardResponseBodies`, JMeter MD5-only responses)

`127.0.0.1` is used instead of `localhost`: `localhost` resolves to IPv6
first on Windows, and failed IPv6 dials inflate error counts.

## Running

Start the server:

```bash
go run ./benchmarks/server -addr 127.0.0.1:8080 -delay 10ms -body-size 2
```

Then run each tool with the same VUs and duration:

```bash
go build -o bin/loadtool ./cmd/loadtool
./bin/loadtool run benchmarks/loadtool/scenario.ts --vus 1000 --duration 60s

k6 run --vus 1000 --duration 60s benchmarks/k6/scenario.js

jmeter -n -t benchmarks/jmeter/scenario.jmx -Jvus=1000 -Jduration=60 -l jmeter.jtl
```

- Measure peak process memory the same way for every tool.
  [`loadtool/peak-memory.ps1`](loadtool/peak-memory.ps1) samples it on
  Windows; for JMeter, measure the `java` process.

## Known limits

- **Windows accept backlog.** A local Windows server refuses connections
  when about 1,000 clients connect at the same instant (see
  [results/2026-09-24-script-execution.md](results/2026-09-24-script-execution.md)).
  For 1,000-VU runs, put the server on a separate Linux machine and point
  the scenarios at it:
  - LoadTool: edit `TARGET` in the scenario file.
  - k6: `-e TARGET=http://<host>:8080/`
  - JMeter: `-Jhost=<host>`
- The JMeter plan was written by hand and has not yet been run with
  JMeter, because JMeter is not installed on the development machine.

## What every result must record

- **Environment:** CPU, RAM, OS; Go, k6, JMeter and Java versions.
- **Setup:** where the server ran and its `-delay` and `-body-size`.
- **Run:** the scenario, VU count, duration and request count.
- **Measurements:** throughput, error rate and latency percentiles, plus
  peak memory and CPU usage for each tool.
- **Repeats:** at least 3 runs per tool, alternating tools.
