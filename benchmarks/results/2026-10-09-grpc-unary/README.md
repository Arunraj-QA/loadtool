# 2026-10-09 — gRPC unary calls at 10 to 1,000 VUs

The first measurement of the gRPC module (ADR-020): unary calls at 10,
100, 500 and 1,000 VUs. It is a baseline for this module, not a
comparison with other tools.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`PowerOnline: True`); no other work was started during the runs |
| Target | The demo API's greeter (`bin/demo-api.exe -grpc-addr 127.0.0.1:8091 -delay 10ms`) on the **same machine**: `SayHello` waits 10 ms, like the HTTP benchmark target |
| LoadTool | branch `p2/grpc`: session 1 at `ac64807`; session 2 with the connect fix (ADR-020 amendment) |
| Go | go1.27.0 windows/amd64 |

## Method

```powershell
./benchmarks/grpc-unary.ps1 -OutDir <dir>     # VUs 10, 100, 500, 1000; 30 s runs; 3 rounds; 10 s warm-ups
```

**The scenario** (`benchmarks/loadtool/grpc-unary.ts`):

- Each VU has its own client: one gRPC connection per VU, as in scope
  decision 3.
- Each VU connects until connected, then calls `SayHello` in a loop and
  throws on a status that is not OK.

**The harness:**

1. One warm-up per level, then three rounds with the levels in turn.
2. Calls, rates, latency and failures are read from LoadTool's JSON
   summary (`grpc_*` metrics).
3. Peak private memory, working set and CPU (as a percentage of the
   whole machine) are sampled from the LoadTool process every 200 ms.

Every run is in `raw/runs.jsonl`, with each run's log and JSON summary.

## Session 2: the measurements

Medians of three 30 s runs, with ranges:

| VUs | Calls/s | p50 ms | p95 ms | p99 ms | Failed | Peak private MB | CPU % of machine |
|---|---|---|---|---|---|---|---|
| 10 | 898 (844–904) | 11.08 (10.94–11.47) | 11.86 (11.73–13.43) | 12.39 (12.26–17.17) | 0 | 64.0 (63.9–64.3) | 3.5 (3.4–4.5) |
| 100 | 9,116 (8,960–9,200) | 10.81 (10.68–10.94) | 11.99 (11.86–12.65) | 13.43 (12.78–15.14) | 0 | 88.8 (88.6–89.0) | 23.6 (22.4–24.7) |
| 500 | 17,526 (15,933–17,540) | 23.20 (23.20–24.77) | 63.18 (59.51–71.83) | 87.56 (86.51–108.53) | 0 | 203.1 (201.9–203.6) | 46.5 (42.4–46.5) |
| 1,000 | 12,725 (11,936–13,243) | 57.93 (57.41–58.46) | 191.89 (175.11–225.44) | 265.29 (242.22–362.81) | 0 | 351.9 (332.1–357.8) | 44.6 (43.8–45.1) |

**There were no failures and no script errors** in any run: 358,725 to
526,596 calls per 30 s run at 500 and 1,000 VUs.

### What the numbers show

- **10 and 100 VUs run at the server's pace.** With a 10 ms server
  delay, each VU can make at most about 90 calls/s, so 10 and 100 VUs
  can reach about 900 and 9,000. The measured rates match, and p50 stays
  at the delay (about 11 ms).
- **500 and 1,000 VUs are past this setup's capacity.**
  - Calls per second (17.5k, 12.7k) are well below 45k and 90k.
  - Latency rises (p50 23 ms and 58 ms against a 10 ms server).
  - The rate falls from 500 to 1,000 VUs.
- **The bottleneck was not measured.** LoadTool and the target share one
  laptop. The LoadTool process used about 45 % of the machine at both
  levels; the server's own CPU was not sampled. Separating client and
  server needs two machines or server sampling, which is not part of this
  run.
- **Memory grows with VUs:** from 64 MB at 10 VUs to 352 MB at 1,000. From
  500 to 1,000 VUs it grew about 0.3 MB per VU. That is consistent with
  each VU holding its own gRPC connection (buffers and HTTP/2 state).
  This is input to scope decision 3 (one connection per VU), which ADR-020
  says is confirmed by measurement. A comparison with a shared connection
  has not been run.

## Session 1: invalid, kept for the record

**Session 1** (`raw-session1/`) **failed about 95 % of calls at 500 and
1,000 VUs**, with `Error: connect: could not connect to 127.0.0.1:8091`.
Its 500 and 1,000 VU rates (73k–141k calls/s) were these failures,
not throughput. Two things combined:

1. **`connect` gave up on the first failed attempt.** When hundreds of
   VUs connect at once, some attempts are refused while the server's
   listen backlog is full.
2. **The scenario connected only when `__ITER === 0`.** After one failed
   connect, every later iteration called an unconnected client and failed
   at once.

**Both were fixed before session 2** (ADR-020 amendment):

- `connect` now waits for grpc-go's reconnect until its timeout;
- scripts connect until connected;
- `TestConnectRetriesUntilTimeout` covers it.

Session 1's 10 and 100 VU results agree with session 2's.

## Caveats

- One laptop, with the target on the same machine, so the rates at 500
  and 1,000 VUs describe this setup's capacity, not LoadTool's alone.
- Three runs per level.
