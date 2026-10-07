# 2026-10-07 — HTTP/1.1 vs HTTP/2

What HTTP/2 changes for LoadTool's HTTP client (ADR-010):

1. the cost per request;
2. the connections opened;
3. behaviour at 1,000 concurrent requests and against servers with
   stream limits.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 16 GB, Windows 11 Enterprise 10.0.26300, on AC power |
| Go | go1.27.0 windows/amd64 |
| Code | branch `p1/http2-hardening` |

## 1. Cost per request (`BenchmarkProtocols`)

```bash
go test -run '^$' -bench BenchmarkProtocols -benchtime=2s -count=3 ./internal/httpclient/
```

**Setup:**

- **Server.** A local server, in the same process, answers a 70-byte JSON
  body.
- **Connections.** They are made before timing starts, so handshakes are
  not measured.
- **Modes.** "serial" is one request at a time; "parallel" is about 64
  requests in flight on a client allowing 64 connections.
- **Raw output** is in `bench.txt`.

### Measured

Medians of 3 runs:

| Protocol | Mode | ns/op | B/op | allocs/op | Server connections |
|---|---|---|---|---|---|
| HTTP/1.1 cleartext | serial | 119,929 | 6,551 | 75 | 1 |
| HTTP/1.1 cleartext | parallel | 12,522 | 6,543 | 73 | 64 |
| h2c | serial | 201,230 | 9,515 | 90 | 1 |
| h2c | parallel | 37,122 | 9,533 | 89 | 1 |
| HTTP/1.1 TLS | serial | 203,540 | 6,561 | 74 | 1 |
| HTTP/1.1 TLS | parallel | 31,377 / 35,262 ¹ | 6,690–6,729 | 73–74 | 64 |
| HTTP/2 TLS | serial | 282,593 | 8,460 | 76 | 1 |
| HTTP/2 TLS | parallel | 40,651 | 8,462 | 75 | 1 |

¹ The third run's line was cut by the test server's log output (since
silenced); a separate rerun measured 17,103 ns/op.

### Observations

- **Allocations are stable between runs; times are not.** HTTP/1.1 TLS
  parallel measured 13,036 ns/op in an earlier session the same morning
  and 31,377–35,262 here. Compare protocols within one run, and treat
  timings as rough ratios.
- **HTTP/2 costs more per request in this setup.**
  - Time: about 1.2–1.4× over TLS and 1.7–3× in cleartext.
  - Memory: about 1.9 KB more over TLS, about 3 KB more for h2c.
  - The server's work is in the same process, so the cost is not the
    client's alone.
- **HTTP/2 uses one connection where HTTP/1.1 uses 64.** This is its main
  effect for a load test:
  - far fewer TCP and TLS handshakes;
  - far fewer connections for the server to hold.
- **Loopback has no network latency.** Over a real network, where a
  request waits milliseconds, the client's microseconds per request
  matter less; connection counts matter more.

## 2. 1,000 concurrent requests over TLS

**Method.** A throwaway test (not committed) against a local HTTPS server
with HTTP/2:

- 1,000 goroutines making 5 requests each;
- the server answers after 10 ms;
- the client allows 1,000 connections;
- "warm" means one request was made first.

Two rounds:

| Protocol | Start | Connections | Errors (of 5,000) |
|---|---|---|---|
| HTTP/1.1 | cold | 340 / 307 | 4,264 / 4,100 |
| HTTP/1.1 | warm | 326 / 294 | 4,339 / 4,413 |
| HTTP/2 | cold | 218 / 203 | 1,305 / 104 |
| HTTP/2 | warm | 105 / 62 | 0 / 0 |

**Observations:**

- **HTTP/1.1 fails here because of the setup.** The errors are refused or
  failed connections: one Windows laptop can't accept and handshake 1,000
  TLS connections at once. That is the known limit of running the target
  on the same machine (review item M6), not a LoadTool bug.
- **HTTP/2 avoids most of it.** Once a connection exists it multiplexes,
  and there were no errors. When every connection is at the server's
  stream limit, Go opens more connections (default, non-strict mode).

**Strict mode was rejected.** Go's `StrictMaxConcurrentRequests`, which
queues requests instead of opening connections, made the same test take
15–25 s instead of 0.2 s, with 4,124–4,929 errors (mostly timeouts).

## 3. Servers with low stream limits

- **The cause.** Until a new connection receives the server's settings,
  Go's client assumes the server allows 100 concurrent streams.
- **The effect.** A server that allows fewer, such as 2, can refuse the
  first streams on a new connection (`PROTOCOL_ERROR`). Those requests
  fail and are counted (`TestHTTP2LowStreamLimitIsCounted`).
- **When it matters.** Servers following RFC 9113's recommendation of at
  least 100 streams are not affected.

## 4. Overflowing a server's stream limit: a dial burst

When every connection is at the server's stream limit, Go dials a new
connection for **each** request waiting for a stream.

**Measured.** A throwaway test (not committed), 6 rounds:

- the server allows 100 streams per connection;
- 400 requests start at once, after one warm-up request;
- the client allows 400 connections;
- Go was limited to 2 CPUs (`-cpu=2`), as on a CI runner.

**Result:** 93–114 of the 400 requests failed with "connection refused".
Up to 300 simultaneous dials overflowed the local server's accept queue
while it accepted slowly; 14–46 connections were opened.

**How this compares:**

- HTTP/1.1 opens one connection per VU anyway, so the burst is not worse
  than HTTP/1.1's start-up.
- But a test that pushes an HTTP/2 server past its stream limit gets a
  burst of new connections, which a real server may also refuse.
- With an overflow of 50 requests the same test had no errors (5 runs at
  2 CPUs). That is what `TestHTTP2ConcurrentStreamLimit` now uses.

## 5. A target that refuses connections

**What ran.** 1,000 VUs for 8 s against a port nothing listens on, with
`httpVersion` `"1.1"` and `"2"`, watching the process's thread count:

| httpVersion | Peak threads | Requests (all failed) | Exit code |
|---|---|---|---|
| `"1.1"` | 1,009 | 3,537 | 0 |
| `"2"` | 1,014 | 14,673 | 0 |

**Result:**

- Both stay around one thread per VU blocked in a connection attempt, far
  below Go's 10,000-thread limit.
- HTTP/2 does not need the per-host connection cap that protects
  HTTP/1.1 from this case (ADR-003).
