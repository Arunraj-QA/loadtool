# ADR-013: Discard response bodies by default

- Status: Accepted
- Date: 2026-10-07
- Supersedes: the `discardResponseBodies` default in ADR-008

## Context

ADR-008 kept response bodies by default, as k6 does, so scripts can read
`res.body` and `res.json()`. Phase 0 had always discarded them.

A user reported much higher memory for Phase 1 than for Phase 0 at 1,000
VUs. The Phase 1 exit benchmark had not shown it, because its scenario
discards bodies and its responses are 66 bytes. With large responses the
cost is large:

- **Each VU holds its current body.** 1,000 VUs with 1 MB responses keep
  about 1 GB live.
- **The heap peaks at about twice that.** Go's collector lets the heap
  grow to about twice the live data before collecting.
- **Throughput drops.** Allocating and collecting the bodies about halves
  requests per second.

Measured on 2026-10-07 at 1,000 VUs against 1 MiB responses: 30 s per
run, three runs per case interleaved in one session, on AC power (see
`benchmarks/results/2026-10-07-response-bodies/`). Medians, with ranges:

| Build | Peak private | Requests (30 s) |
|---|---|---|
| Phase 0 (`3b7b523`, always discarded) | 179 MB (170–192) | 120,703 (109,608–130,686) |
| Phase 1 before this change (bodies kept) | 1,959 MB (1,807–2,040) | 72,744 (66,112–73,211) |
| This change, default (discarded) | 176 MB (172–238) | 118,272 (115,540–118,590) |
| This change, `discardResponseBodies: false` | 2,044 MB (2,043–2,046) | 72,274 (60,159–73,546) |

**The reading code was not the problem.** It already sized the buffer
from `Content-Length` and converted the body to a string only when the
script read it. The cost is that of keeping bodies at all.

## Decision

1. **`discardResponseBodies` defaults to `true`.**
   - Each body is still read in full, so timings include it, but it is
     not kept.
   - `discardResponseBodies: false` keeps every body, as before.
2. **`params.responseType` overrides it for one request.**
   - `"text"` keeps that body; `"none"` discards it.
   - Other values are a `TypeError`. k6's `"binary"` is not supported.
   - A script can therefore keep only the bodies it checks.
3. **A discarded body says why it is missing.**
   - Reading `res.body` warns once per run, naming both ways to keep it.
   - `res.json()` throws a `TypeError` with the same advice, so a check
     that uses it fails with that message.
4. **`setup` and `teardown` keep bodies, as before** (ADR-008), because
   setup typically reads a token.
5. **Regression guards:**
   - `TestDiscardedBodiesAreNotAllocated` (in `internal/script`) asserts
     that bytes allocated per iteration stay under 64 KiB with 1 MB
     responses when bodies are discarded, and reach at least 512 KiB when
     they are kept. It measures per iteration of an existing VU, because
     an earlier per-run version counted start-up costs and failed under
     the race detector in CI. `TestRunAppliesHTTPOptionsAndWarnings` (in
     `internal/runner`) checks that the default discards end to end.
   - `benchmarks/body-memory.ps1` measures peak process memory against
     the benchmark server's new `GET /api/large`.

## Consequences

- **Scripts that read bodies must opt in.** This differs from k6 and is
  listed in `docs/k6-differences.md`. The warning and the `TypeError`
  make the change visible instead of silent.
- **Memory no longer grows with response size by default.** Peak memory
  and requests are back within the Phase 0 build's range.
- **The examples that read bodies opt in:**
  - `checks.ts` keeps all bodies;
  - `post-json.ts`, `auth-token.ts`, `sessions.ts` and `lib/api.ts` keep
    them per request.
- **The Phase 0 and Phase 1 exit benchmarks are unaffected.** Their
  scenario already set `discardResponseBodies: true`.
- **A gap in the exit benchmark is closed:** the large-response case is
  now part of the benchmarks.
