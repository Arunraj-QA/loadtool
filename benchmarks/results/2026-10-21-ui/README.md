# 2026-10-21 — Status-code counting on the HTTP path (ADR-023)

`loadtool ui` needs responses counted by status code, so the HTTP
recording path gained one call: `Recorder.RecordStatus`, one atomic
increment on a counter in the recorder's shared shard. This checks that
it costs nothing measurable.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power | **On AC power** (`PowerLineStatus: Online`) |
| Builds | base: `0b8f9b4` (the audited Phase 2 build); new: the working tree before commit `11d6ceb` (status counting) |
| Go | go1.27.0 windows/amd64 |

## Method

Test binaries were built from both trees. Five rounds alternated the
order (base first in odd rounds), each running:

- `BenchmarkDo` (`internal/httpclient`): one HTTP request through
  `httpclient.Do`, the path that now records the status;
- `BenchmarkIterateHTTPGet` (`internal/script`): one VU iteration making
  one `http.get`.

Both use `-benchmem -benchtime 2s`, against a local test server. Raw
output is in `micro-status-codes.txt`.

## Measured

Medians of five runs, with ranges:

| Benchmark | Build | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| `BenchmarkDo` | base | 86,150 (83,900–92,151) | 5,736–5,745 | 69 |
| `BenchmarkDo` | new | 85,193 (81,497–91,819) | 5,740–5,741 | 69 |
| `BenchmarkIterateHTTPGet` | base | 100,658 (87,272–101,626) | 6,487–6,492 | 81 |
| `BenchmarkIterateHTTPGet` | new | 95,129 (91,621–101,058) | 6,482–6,493 | 81 |

**No measurable cost:**

- the same allocations;
- the same bytes per operation, within a few bytes of noise;
- time ranges that overlap. The new build's medians are slightly lower,
  which is noise.

The counters add no memory per VU. They live in the 16 shared shards,
4.8 KB each, about 77 KB per run whatever the VU count.

## Not yet measured

This covers the request path, not a whole run. For Phase 2 exit
criterion 5 to say `loadtool run` is unchanged, a regression run at
1,000 VUs against the Phase 2 build, measured as for criterion 2, would
confirm it at the process level.
