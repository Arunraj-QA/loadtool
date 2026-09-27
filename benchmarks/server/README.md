# Benchmark target server

A small, deterministic HTTP server that LoadTool, k6 and JMeter all run
against, so the tools are compared on exactly the same target.

It uses only the Go standard library and imports nothing from LoadTool;
`TestImportsStandardLibraryOnly` enforces this. It has no database, makes
no outbound network calls, and is not tuned for any particular client: it
is plain `net/http` with default settings.

## Start

From the repository root:

```bash
go run ./benchmarks/server -addr 127.0.0.1:8080 -delay 10ms
```

For benchmark runs, build a binary first so compile time and the `go`
tool are not part of the measurement:

```bash
go build -o bin/benchserver ./benchmarks/server
./bin/benchserver -addr 0.0.0.0:8080 -delay 10ms
```

Stop it with Ctrl+C. In-flight requests get up to 5 seconds to finish.

Check that it is up:

```bash
curl -i http://127.0.0.1:8080/health
```

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:8080` | Listen address. Use `0.0.0.0:8080` when load comes from another machine. |
| `-delay` | `10ms` | Fixed wait before `/api/test` responds. `0` disables it. |

## Endpoints

| Request | Response |
|---|---|
| `GET /api/test` | `200`, `application/json`, after `-delay` |
| `GET /health` | `200`, `{"status":"ok"}`, immediately |
| `HEAD` on either path | `200`, headers only |
| Any other path | `404` |
| Any other method | `405` |

The `/api/test` body is always:

```json
{"id":1,"name":"loadtool-benchmark","status":"ok","items":[1,2,3]}
```

## Deterministic behaviour

- Every `/api/test` response has the same status, headers and body
  (66 bytes, with a fixed `Content-Length`). There are no timestamps, IDs
  or counters.
- The automatic `Date` header is suppressed, so responses do not change
  over time.
- The delay is the same for every request. A request cancelled by the
  client during the delay gets no response.
- There is no per-request logging, so the server does not slow down
  because of I/O.
- `/health` does not use the delay, so readiness checks stay fast.

The response time seen by a client is `-delay` plus network and
scheduling time. Only the delay is controlled; the rest depends on the
machine, which is why the environment must be recorded with every result.
