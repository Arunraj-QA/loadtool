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

## Whole-run A/B at 1,000 VUs: inconclusive (machine not idle)

`ab.ps1` (this folder) ran the audited Phase 2 build (`0b8f9b4`, "base")
against the UI build (`496e200`, "new"). It used the HTTP exit scenario:
1,000 VUs, 60 s, three rounds alternating the order, `benchmarks/server`
with a 10 ms delay. Raw data is in `raw-ab/`.

The rule, as for criterion 2: the two builds' ranges overlap, and the
medians differ by no more than the wider range.

| | base runs | new runs | Median Δ | Wider range | By the rule |
|---|---|---|---|---|---|
| Peak private MB | 202.9, 196.9, 186.5 | 189.9, 199.3, 185.5 | −7.0 | 16.4 | within |
| CPU % of machine | 34.6, 30.1, 29.2 | 28.5, 29.1, 28.2 | −1.6 | 5.4 | outside, but **lower** for the new build |
| Requests/s | 40,675, 27,753, 25,598 | 19,515, 27,004, 21,775 | −5,977 (−22 %) | 15,077 | within |

All runs exited 0, with errors of 0.12–0.32 %.

**This run does not meet the benchmark conditions.** The laptop was on
AC power, but not idle:

- **Background CPU** was 37–57 % before the runs and 27–56 % after.
  Microsoft Defender (`mssense`, `MsMpEng`) was scanning, probably the
  newly built binaries. In the 2026-10-14 sessions it was 5–36 %.
- **Throughput fell for both builds:** 20,000–41,000 req/s, against
  45,000–49,000 on 2026-10-14. That points to the machine, not a build.

So it neither shows nor rules out a change in throughput.

**What it does show:**

- **Memory per run is unchanged**, within variation.
- **The request path costs the same** (the micro-benchmarks above).

**Still needed for criterion 5:** run `ab.ps1` again once the scanning
has stopped and background CPU is back near the 2026-10-14 level.
