# Phase 0 — Performance Testing Tool Architecture Skill

## 1. Purpose

This skill defines the architecture, technology choices, development standards, testing strategy, and implementation guidelines for **Phase 0** of the Performance Testing Tool.

The goal of Phase 0 is to establish a minimal but production-oriented foundation that can:

* Execute a TypeScript test script.
* Create virtual users (VUs).
* Execute HTTP/1.1 requests.
* Run JavaScript/TypeScript logic inside Go.
* Execute multiple VUs concurrently using Go goroutines.
* Provide a CLI interface.
* Produce basic execution metrics.
* Be benchmarked against established tools.
* Maintain a clean architecture for future phases.
* Be developed and maintained using Claude Code.

---

# 2. Phase 0 Technology Stack

| Component     | Phase 0 Choice             |
| ------------- | -------------------------- |
| Language      | Go                         |
| CLI           | Cobra                      |
| JS/TS Runtime | goja                       |
| Load Model    | Goroutine per VU           |
| Protocol      | HTTP/1.1                   |
| Test File     | TypeScript                 |
| Repository    | GitHub                     |
| License       | Apache 2.0                 |
| Testing       | Go `testing`               |
| Benchmark     | Go Benchmark + k6 + JMeter |
| AI Coding     | Claude Code                |

---

# 3. Architecture Principles

The following principles MUST be followed.

## 3.1 Simplicity First

Phase 0 must implement the smallest architecture capable of executing a realistic load test.

Do NOT introduce:

* Distributed execution
* Kubernetes
* Message queues
* Databases
* Microservices
* Complex orchestration
* Cloud-specific infrastructure
* Persistent result storage

unless explicitly required by a later phase.

---

## 3.2 Go as the Core Engine

Go is the primary implementation language.

Go should be responsible for:

* CLI execution
* Test lifecycle
* VU management
* Scheduling
* HTTP execution
* Metrics collection
* Error handling
* Runtime integration
* Concurrency
* Benchmarking

The JavaScript runtime must NOT become the core execution engine.

---

# 4. Go

## Role

Go is the core language for the performance-testing engine.

## Why Go

Go provides:

* Lightweight concurrency
* Goroutines
* Channels
* Fast startup
* Low memory overhead
* Strong standard library
* Native HTTP support
* Excellent benchmarking support
* Easy CLI distribution
* Cross-platform compilation

## Coding Standards

Use:

* `gofmt`
* `go vet`
* idiomatic Go
* explicit error handling
* small interfaces
* dependency injection where useful
* table-driven tests

Avoid:

* unnecessary abstractions
* global mutable state
* excessive interfaces
* reflection unless required
* premature optimization

## Recommended Structure

```text
cmd/
internal/
    engine/
    runtime/
    http/
    metrics/
    config/
    executor/
pkg/
scripts/
tests/
benchmarks/
docs/
```

---

# 5. CLI — Cobra

## Role

Cobra provides the command-line interface.

The primary executable should be:

```text
pt
```

Example:

```bash
pt run script.ts
```

Potential Phase 0 commands:

```bash
pt run script.ts
pt version
pt help
```

Future commands may include:

```bash
pt validate script.ts
pt benchmark
pt report
pt inspect
```

## CLI Requirements

The CLI MUST:

* return meaningful exit codes
* validate arguments
* display useful errors
* support `--help`
* support `--version`
* avoid leaking internal implementation details
* keep CLI logic separate from engine logic

Example:

```bash
pt run script.ts --vus 100 --duration 30s
```

---

# 6. JavaScript Runtime — goja

## Role

`goja` provides the JavaScript execution environment inside Go.

The runtime allows users to write test scenarios using JavaScript/TypeScript syntax while the underlying load engine remains implemented in Go.

## Architecture

```text
TypeScript Test
      |
      v
JavaScript Runtime
      |
      v
Go API Bridge
      |
      v
Go Load Engine
      |
      v
HTTP Client
```

## Runtime Responsibilities

The runtime should:

* load test scripts
* execute JavaScript
* expose controlled Go APIs
* execute requests
* return responses
* expose basic test utilities

The runtime should NOT:

* manage global VU scheduling
* create operating-system processes per VU
* own the entire load engine
* directly manage infrastructure

---

# 7. TypeScript Test Files

## Role

Users define load-test scenarios using TypeScript.

Example:

```typescript
export default function () {
    const response = http.get("https://example.com");

    check(response, {
        "status is 200": response.status === 200
    });
}
```

Phase 0 may initially transpile TypeScript to JavaScript before execution if required.

The architecture should keep TypeScript compilation separate from the runtime.

```text
script.ts
   |
   v
TypeScript Compiler
   |
   v
JavaScript
   |
   v
goja
   |
   v
Go Engine
```

## Test Script Principles

Test scripts should be:

* deterministic where possible
* easy to read
* reusable
* isolated from engine internals
* independent of infrastructure

---

# 8. Load Model — Goroutine per VU

## Phase 0 Model

Each Virtual User is represented by a Go goroutine.

```text
Engine
 |
 +-- VU 1 -> Goroutine
 |
 +-- VU 2 -> Goroutine
 |
 +-- VU 3 -> Goroutine
 |
 +-- VU N -> Goroutine
```

Example:

```go
for i := 0; i < vus; i++ {
    go runVU(ctx, i)
}
```

## VU Responsibilities

A VU should:

1. Initialize runtime context.
2. Execute the test function.
3. Execute requests.
4. Collect metrics.
5. Repeat according to the configured load model.
6. Stop when the test context is cancelled.

## Important Rule

Do not create one OS thread per VU.

Use Go goroutines.

---

# 9. HTTP/1.1

## Role

HTTP/1.1 is the Phase 0 protocol.

The implementation should initially use Go's standard HTTP client.

Example:

```go
client := &http.Client{}

req, err := http.NewRequest(
    http.MethodGet,
    url,
    nil,
)

resp, err := client.Do(req)
```

## Requirements

Support:

* GET
* POST
* PUT
* PATCH
* DELETE
* headers
* query parameters
* request body
* response status
* response body
* request duration
* errors
* timeouts

## Connection Reuse

HTTP connection reuse should be enabled where appropriate.

Do not create a new HTTP client for every request.

Prefer reusable clients/transports.

---

# 10. Metrics

Phase 0 should collect at minimum:

```text
request_count
success_count
error_count
duration
status_code
bytes_sent
bytes_received
```

Basic latency statistics:

```text
min
max
average
p50
p90
p95
p99
```

Example:

```text
Requests:       10,000
Success:         9,950
Errors:             50

Latency:
p50:             45ms
p90:             82ms
p95:            110ms
p99:            210ms
```

Metrics collection must not introduce significant contention.

---

# 11. Testing — Go testing

Use the standard Go testing framework.

Tests should exist at multiple levels.

## Unit Tests

Test:

* configuration
* CLI parsing
* runtime APIs
* HTTP client
* metrics
* VU lifecycle
* error handling

Example:

```go
func TestHTTPGet(t *testing.T) {
    // test implementation
}
```

## Integration Tests

Validate:

```text
CLI
 |
Engine
 |
Runtime
 |
HTTP
 |
Metrics
```

Use local HTTP test servers wherever possible.

Example:

```go
server := httptest.NewServer(handler)
defer server.Close()
```

Do not depend on external internet services for normal automated tests.

---

# 12. Go Benchmarks

Go benchmarks are required to measure the performance of the engine itself.

Example:

```go
func BenchmarkHTTPRequest(b *testing.B) {
    for i := 0; i < b.N; i++ {
        // execute operation
    }
}
```

Measure:

* request execution overhead
* runtime execution overhead
* VU creation overhead
* metrics overhead
* script execution overhead
* memory allocation
* throughput

Useful command:

```bash
go test -bench=. -benchmem ./...
```

---

# 13. Benchmark Comparison

Phase 0 performance should be compared against:

* Go implementation
* k6
* JMeter

The objective is NOT to copy the internal architecture of k6 or JMeter.

The objective is to establish measurable baseline characteristics.

## Compare

| Metric                    | Tool             |
| ------------------------- | ---------------- |
| Startup time              | Go / k6 / JMeter |
| Memory                    | Go / k6 / JMeter |
| CPU                       | Go / k6 / JMeter |
| Requests/sec              | Go / k6 / JMeter |
| Latency overhead          | Go / k6 / JMeter |
| VU scalability            | Go / k6 / JMeter |
| Script execution overhead | Go / k6 / JMeter |

Benchmark environments must be identical.

Record:

* CPU
* RAM
* OS
* Go version
* k6 version
* JMeter version
* test scenario
* number of VUs
* duration
* request count

---

# 14. GitHub Repository

The source code should be hosted in GitHub.

Recommended repository:

```text
performance-testing-tool
```

Recommended structure:

```text
performance-testing-tool/
│
├── .claude/
│   └── skills/
│
├── .github/
│   └── workflows/
│
├── cmd/
├── internal/
├── pkg/
├── tests/
├── benchmarks/
├── examples/
├── docs/
│
├── go.mod
├── go.sum
├── LICENSE
├── README.md
└── CONTRIBUTING.md
```

---

# 15. Apache 2.0 License

The project will use:

```text
Apache License 2.0
```

The repository must contain:

```text
LICENSE
```

Source files should include appropriate copyright/license headers if required by the project's chosen conventions.

Do not copy implementation code from k6, JMeter, or other projects unless its license permits such use and the licensing requirements are followed.

---

# 16. Claude Code

Claude Code is the primary AI-assisted development tool for Phase 0.

Claude Code must follow this skill before modifying the project.

## Claude Code Responsibilities

Claude Code can assist with:

* architecture implementation
* code generation
* refactoring
* unit tests
* integration tests
* benchmarks
* documentation
* debugging
* code review
* Git workflows

## Claude Code Rules

Before implementing a feature:

1. Understand the existing architecture.
2. Inspect relevant files.
3. Identify dependencies.
4. Explain the proposed change.
5. Implement the smallest required change.
6. Add or update tests.
7. Run formatting.
8. Run tests.
9. Run static analysis.
10. Review the resulting diff.

---

# 17. Claude Code Development Loop

Use the following loop:

```text
Understand
    |
    v
Inspect Repository
    |
    v
Plan
    |
    v
Implement
    |
    v
Test
    |
    v
Benchmark
    |
    v
Review
    |
    v
Commit
```

Claude Code should NOT blindly generate large amounts of code.

Prefer incremental implementation.

---

# 18. Definition of Done

A Phase 0 feature is complete only when:

* Code compiles.
* Unit tests pass.
* Integration tests pass where applicable.
* `go vet` passes.
* Code is formatted.
* Error handling is implemented.
* Documentation is updated.
* Benchmark impact is understood where applicable.
* No unnecessary dependencies are introduced.
* Git diff has been reviewed.

Recommended commands:

```bash
gofmt -w .
go test ./...
go vet ./...
go test -bench=. -benchmem ./...
```

---

# 19. Phase 0 Acceptance Criteria

Phase 0 is considered successful when the following workflow works:

```text
TypeScript Test
      |
      v
CLI
      |
      v
Go Engine
      |
      v
VU Scheduler
      |
      v
Goroutines
      |
      v
goja Runtime
      |
      v
HTTP/1.1 Client
      |
      v
Target API
      |
      v
Metrics
      |
      v
Console Report
```

Example:

```bash
pt run examples/basic.ts \
   --vus 100 \
   --duration 30s
```

Expected output:

```text
Performance Test

VUs:          100
Duration:     30s

Requests:     125,430
Success:      124,980
Errors:           450

Latency:
p50:          42ms
p90:          78ms
p95:          96ms
p99:         185ms

Throughput:
4,181 req/s
```

---

# 20. Phase 0 Non-Goals

The following are explicitly outside Phase 0:

* Distributed load generation
* Kubernetes execution
* Cloud execution
* Multi-node orchestration
* Web dashboard
* Persistent database
* Advanced HTML reporting
* Distributed metrics aggregation
* Browser testing
* WebSocket testing
* HTTP/2
* HTTP/3
* Advanced arrival-rate scheduling
* Automatic cloud scaling

These should be considered for future phases.

---

# 21. Future Architecture Direction

The Phase 0 architecture must leave room for:

```text
                    ┌───────────────┐
                    │      CLI      │
                    └───────┬───────┘
                            │
                    ┌───────▼───────┐
                    │ Load Engine   │
                    └───────┬───────┘
                            │
             ┌──────────────┼──────────────┐
             │              │              │
        ┌────▼────┐    ┌────▼────┐   ┌────▼────┐
        │   VU    │    │ Metrics │   │ Runtime │
        │ Manager │    │ Engine  │   │         │
        └────┬────┘    └─────────┘   └────┬────┘
             │                            │
             │                       ┌────▼────┐
             │                       │  goja   │
             │                       └────┬────┘
             │                            │
             └────────────┬───────────────┘
                          │
                    ┌─────▼─────┐
                    │ HTTP/1.1  │
                    └───────────┘
```

Future phases may introduce:

```text
Phase 1
  ↓
Advanced metrics

Phase 2
  ↓
Advanced load models

Phase 3
  ↓
Distributed execution

Phase 4
  ↓
Web dashboard

Phase 5
  ↓
Cloud / Kubernetes

Phase 6
  ↓
AI-assisted test generation
```

---

# 22. Architectural Golden Rules

Claude Code MUST follow these rules:

1. Go owns the engine.
2. Goroutines represent VUs.
3. goja executes user JavaScript.
4. TypeScript is the user-facing test language.
5. Cobra owns CLI concerns.
6. HTTP/1.1 is the initial protocol.
7. Metrics must be lightweight.
8. Tests must not depend on external systems.
9. Benchmarks must be reproducible.
10. Keep Phase 0 simple.
11. Avoid premature abstractions.
12. Avoid unnecessary dependencies.
13. Every feature requires tests.
14. Performance-sensitive code requires benchmarks.
15. Do not introduce distributed architecture before it is required.
16. Preserve clear separation between CLI, engine, runtime, protocol, and metrics.
17. Prefer standard Go libraries when they satisfy the requirement.
18. Any architectural change must be documented.
19. Claude Code must inspect existing code before modifying it.
20. Never sacrifice correctness
