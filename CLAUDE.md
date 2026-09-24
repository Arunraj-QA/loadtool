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

**Phase 0 — Foundations**

Phase 0 must remain intentionally small.

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

## Technology Decisions

Language:

* Go

CLI:

* Cobra

JavaScript/TypeScript runtime:

* goja

Initial protocol:

* HTTP/1.1

Load model:

* goroutine per VU

Testing:

* Go testing package

Repository:

* GitHub

License:

* Apache 2.0 unless explicitly changed through an ADR

## Important Architecture Principles

1. Keep Phase 0 simple.
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
17. Create an ADR when making a significant architecture decision.

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

If a requested feature belongs primarily to Phase 1 or later, do not implement it automatically.

Explain why it belongs to a later phase and ask whether it is required for Phase 0.

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
