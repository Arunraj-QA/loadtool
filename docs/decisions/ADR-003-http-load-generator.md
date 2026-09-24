# ADR-003: Minimal HTTP/1.1 load generator

- Status: Accepted
- Date: 2026-09-24
- Previously: `docs/adr/0002-http-load-generator.md` (ADR 0002)

## Context

Phase 0 needs a goroutine-per-VU HTTP/1.1 load generator with request
counts, error rate and latency percentiles. Script execution (goja) is a
separate task, so the engine must work before scripts exist.

## Decision

1. **Packages.**
   - `internal/config` validates the run settings.
   - `internal/engine` schedules VUs.
   - `internal/httpclient` executes requests.
   - `internal/metrics` records and aggregates results.
   - `internal/cli` wires them together and prints the summary.
2. **The engine is protocol-agnostic.** It calls an
   `IterationFunc(ctx, *metrics.Recorder)` in a loop per VU. When goja
   arrives, the iteration will run the script's default function. The
   engine does not change.
3. **Duration and cancellation come from one context.**
   - `engine.Run` derives `context.WithTimeout` from the caller's context.
     Ctrl+C cancels the parent through `signal.NotifyContext`.
   - `Run` returns only after every VU goroutine has exited
     (`sync.WaitGroup`).
4. **Requests cut off by the end of the test are not recorded.** A request
   whose context was cancelled (deadline or Ctrl+C) is dropped instead of
   being counted as an error. Stopping a test must not create errors.
5. **Metrics are per VU with no locks.**
   - Each VU owns a `metrics.Recorder`, so recording needs no locks or
     atomics.
   - Recorders are merged once, after all VUs stop.
   - All latency samples are kept exactly, and percentiles use the
     nearest-rank method.
6. **HTTP client.**
   - One shared `http.Client` per run, with idle connections per host
     equal to the VU count, so each VU can keep its connection alive.
   - HTTP/2 negotiation is disabled, so HTTPS also uses HTTP/1.1.
   - Redirects are not followed, so one iteration is one measured request.
   - The response body is fully drained, so the connection can be reused.
   - Success means no transport error and a status below 400.
   - The request timeout is 30 s.
7. **Latency is measured** from just before `client.Do` until the body is
   drained and closed. This includes connection setup when a new
   connection is dialed.

## Consequences

- Memory for latency samples grows by 8 bytes per request. For example,
  1,000 VUs at 40k req/s for 10 minutes is about 190 MB. The planned fix
  is a fixed-size histogram (e.g. HDR), which gives constant memory at the
  cost of bounded precision. It should be done before long soak tests and
  before the JMeter memory comparison, since sample storage would
  otherwise dominate the measurement.
- Only error counts are kept, not error types or status codes, so a
  summary cannot yet say *why* requests failed.
- All VUs start at the same instant and retry immediately after a fast
  failure (e.g. connection refused). This can overflow a target's accept
  backlog and inflate error counts. Ramp-up and think time belong to
  script APIs (`sleep`) and later load models.
- On Windows, Go's monotonic clock has coarse resolution (about 0.3–0.5 ms
  observed), so sub-millisecond latencies are quantized.
