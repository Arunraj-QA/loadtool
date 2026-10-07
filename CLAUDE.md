# LoadTool

## Project Purpose

LoadTool is a custom, open-source API performance and load-testing engine written in Go.

The long-term goal is to build a differentiated performance-testing platform with:

* High-performance Go load generation
* TypeScript test-as-code
* Multiple protocol modules
* Distributed execution
* Real-time analytics
* AI-assisted performance analysis
* Browser/mobile performance testing
* No-code test recording
* CI/CD integrations

## Current Phase

We are currently implementing:

**Phase 1 — MVP Core Engine**

Phase 1 extends the Phase 0 engine. Do not rewrite or redesign it: keep
the Phase 0 components that work and extend them (see `docs/architecture.md`).

### Phase 1 Goals

1. Full TypeScript test-as-code DSL
2. setup/teardown hooks
3. checks/assertions
4. thresholds
5. scenarios:

   * ramping
   * constant-VU
   * constant-arrival-rate
6. HTTP/2
7. connection reuse
8. cookie/session handling
9. JSON output
10. self-contained HTML report
11. GitHub Action
12. generic CI recipe
13. documentation
14. 5–10 example scripts
15. preparation for external users

### Phase 1 Exit Criteria

Agreed on 2026-10-07, before the Phase 1 benchmark run:

1. **Phase 0 still holds:** at 1,000 VUs, LoadTool's memory is lower than
   JMeter's, measured the same way as on 2026-10-01
   (`benchmarks/measure.ps1`, same scenario and settings).
2. **No regression:** at 1,000 VUs, LoadTool's peak memory and requests
   per second are within run-to-run variation of the Phase 0 build for
   the same scenario, measured in the same session.
3. **Phase 1 features work end to end:** every local example passes in CI
   (`scripts/smoke-examples.sh`), and the GitHub Action self-test passes.

Benchmarks run on AC power with the machine otherwise idle. Do not claim
Phase 1 is complete until all three are measured and recorded.

Status: all three met, measured on 2026-10-07 in
`benchmarks/results/2026-10-07-phase1/`:

1. At 1,000 VUs LoadTool's peak private memory was 183–196 MB against
   JMeter's 1,367–1,380 MB on every run.
2. Against the Phase 0 build in the same session, peak memory was lower
   (179–192 MB vs 213–229 MB) and requests per second were within
   run-to-run variation.
3. The examples smoke test and the Action self-test pass in CI.

Caveats as in Phase 0: one laptop with the server on the same machine,
and JMeter with its default JVM settings.

## Phase 0 — Foundations (complete)

### Phase 0 Goals

1. Create Go module and CLI skeleton
2. Create repository structure
3. Add permissive open-source license
4. Implement `loadtool run`
5. Implement basic configuration loading
6. Implement HTTP/1.1 load generation
7. Use goroutine-per-VU architecture
8. Execute a TypeScript test file through goja
9. Collect basic:

   * request count
   * success count
   * error count
   * error rate
   * latency
   * p50
   * p90
   * p95
   * p99
10. Print a useful console summary
11. Add unit tests
12. Add benchmarks
13. Compare LoadTool against k6 and JMeter
14. Record CPU and memory measurements

## Phase 0 Exit Criteria

`loadtool run` must execute a scripted HTTP test at 1,000 VUs from a laptop with lower memory usage than JMeter at the same VU count.

Do not claim the exit criteria is met until the benchmark is actually executed and the measurements are recorded.

Status: measured on 2026-10-01 in
`benchmarks/results/2026-10-01-phase0-all-tools/`. At 1,000 VUs LoadTool's
peak private bytes (190–210 MB) and working set were below JMeter's
(1,366–1,371 MB) on every run. Caveats: one laptop with the server on the
same machine, and JMeter with its default JVM settings.

## Technology Decisions

Language:

* Go

CLI:

* Cobra

JavaScript/TypeScript runtime:

* goja

Protocols:

* HTTP/1.1 (Phase 0)
* HTTP/2 (Phase 1)

Load model:

* goroutine per VU
* scenarios run by executors: constant-VUs, ramping, constant-arrival-rate (Phase 1)

Testing:

* Go testing package

Repository:

* GitHub

License:

* Apache 2.0 unless explicitly changed through an ADR

## Important Architecture Principles

1. Keep each phase minimal. Phase 1 extends the Phase 0 engine; do not rewrite it.
2. Do not implement distributed execution yet.
3. Do not implement Kubernetes yet.
4. Do not implement ClickHouse yet.
5. Do not implement Postgres yet.
6. Do not implement React dashboard yet.
7. Do not implement AI analysis yet.
8. Do not implement browser testing yet.
9. Do not implement the no-code recorder yet.
10. Avoid premature abstractions.
11. Prefer small interfaces where they provide clear extension points.
12. Keep protocol-specific code isolated.
13. Every feature must have tests.
14. Performance-sensitive code must have benchmarks.
15. Avoid unnecessary allocations in the hot path.
16. Do not silently change architecture decisions.
17. Create an ADR when making a significant architecture decision. Store it
    as `docs/decisions/ADR-NNN-short-name.md` using the next free number,
    and add it to `docs/decisions/README.md`.

## Development Workflow

For each task:

1. Inspect the existing code.
2. Explain the proposed change briefly.
3. Implement the smallest working change.
4. Add/update tests.
5. Run `go test ./...`
6. Run relevant benchmarks.
7. Run `go vet ./...`
8. Check for race conditions using:
   `go test -race ./...`
9. Review the change for goroutine leaks and unnecessary allocations.
10. Summarize the change and verification results.

## Code Quality

Prefer:

* idiomatic Go
* small functions
* explicit error handling
* context cancellation
* deterministic tests
* meaningful names
* minimal dependencies

Avoid:

* unnecessary frameworks
* global mutable state
* premature abstraction
* hidden goroutines
* ignored errors
* unnecessary reflection
* unnecessary allocations

## Git Workflow

Use small commits.

Suggested commit style:

* `chore: initialize go module`
* `feat: add cli skeleton`
* `feat: add http load generator`
* `feat: add goja script execution`
* `test: add load generator tests`
* `perf: add phase 0 benchmark`
* `docs: document phase 0 benchmark`

Do not combine unrelated changes into one commit.

## Phase Boundary

If a requested feature belongs primarily to Phase 2 or later, do not implement it automatically.

Explain why it belongs to a later phase and ask whether it is required for Phase 1.

## Benchmark Discipline

Performance claims must be based on reproducible measurements.

Never claim:

* "faster"
* "uses less memory"
* "supports 1,000 VUs"
* "beats k6"
* "beats JMeter"

without benchmark evidence.

All benchmark results should be stored under:

`benchmarks/`

## Claude Code Behavior

Act as a senior Go engineer and performance-engineering partner.

Do not blindly generate large amounts of code.

Think about:

* concurrency
* goroutine lifecycle
* connection reuse
* memory allocations
* synchronization
* HTTP client behavior
* latency measurement
* error handling
* cancellation
* benchmark methodology

Before implementing major architecture changes, explain the trade-offs.
