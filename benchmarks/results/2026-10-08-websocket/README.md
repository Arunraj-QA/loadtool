# 2026-10-08 — WebSocket module: concurrency check

A small concurrency benchmark of the WebSocket module (ADR-019). It is a
health check for the first version, not a comparison with other tools.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`PowerOnline: True`); no other work was started during the runs |
| Go | go1.27.0 windows/amd64 |
| Build | branch `p2/websocket` (working tree before its first commit) |

## 1. Go benchmark: sessions in parallel

```bash
go test ./internal/protocols/ws -run '^$' -bench SessionsParallel -benchmem -count=3
```

**Workload:**

- 48 goroutines (4 × GOMAXPROCS). Each is one VU with its own goja
  runtime and module instance, running whole sessions against a local
  echo server (`httptest`).
- One op is one session: connect, 10 messages sent with
  `{ reply: true }` and echoed, then a close handshake.

| Run | ns/op (per session) | B/op | allocs/op |
|---|---|---|---|
| 1 | 255,130 | 106,182 | 946 |
| 2 | 244,324 | 104,239 | 945 |
| 3 | 233,005 | 101,929 | 944 |

That is about 240 µs, 104 KB and 945 allocations per session with 48
sessions at once. Most of the cost is the HTTP upgrade handshake and the
session's JavaScript objects. It is a baseline for later optimization.

## 2. The example at 200 VUs

```powershell
loadtool run examples/websocket.ts --vus 200 --duration 15s
```

**Target:** the demo API (`go run ./examples/server`, default 5 ms per
echo) on the same machine.

**Each iteration:**

1. logs in over HTTP;
2. opens a WebSocket;
3. sends 5 messages with `{ reply: true }`;
4. closes after the 5th reply.

| Measure | Value |
|---|---|
| Sessions | 45,855 (3,048/s), 0 failed |
| Messages | 229,275 sent and received (15,241/s each way) |
| `ws_msg_latency` | avg 17.42 ms, p50 16.71 ms, p95 30.28 ms, p99 36.44 ms |
| `ws_connecting` | avg 22.46 ms, p95 35.39 ms |
| HTTP | 45,855 logins, 0 errors |
| Checks / thresholds | 412,695 of 412,695 checks passed; 4 of 4 thresholds passed |
| Peak private memory | 114.5 MB |
| Peak working set | 79.5 MB |

**Latency reflects the target.** The demo echo waits 5 ms per message
and answers in order, so five pipelined sends see about 5–25 ms (see
ADR-019).

**Caveats:**

- One laptop, with the target on the same machine.
- One run of each.
- No comparison with k6 is made here.
