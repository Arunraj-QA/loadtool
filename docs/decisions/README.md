# Architecture decision records

Significant architecture decisions are recorded here as
`ADR-NNN-short-name.md`. New decisions take the next free number.

| ADR | Decision | Status |
|---|---|---|
| [ADR-001](ADR-001-script-runtime.md) | Script runtime: esbuild transpilation, one goja runtime per VU | Accepted |
| [ADR-002](ADR-002-cli-and-repository-layout.md) | CLI and repository layout | Accepted |
| [ADR-003](ADR-003-http-load-generator.md) | Minimal HTTP/1.1 load generator | Accepted (latency storage superseded by ADR-004; HTTP/2 amended by ADR-010) |
| [ADR-004](ADR-004-latency-histogram.md) | Fixed-size latency histogram | Accepted |
| [ADR-005](ADR-005-k6-shaped-script-api.md) | k6-shaped script API (Phase 1 DSL) | Accepted |
| [ADR-006](ADR-006-options-and-precedence.md) | Run options and their precedence | Accepted |
| [ADR-007](ADR-007-script-modules-and-globals.md) | Script modules and globals: built-ins, imports, `__ENV`, console, sleep | Accepted |
| [ADR-008](ADR-008-test-dsl.md) | Test DSL: responses, checks, thresholds, scenarios, setup/teardown | Accepted |
| [ADR-009](ADR-009-sessions-and-connection-reuse.md) | Sessions (per-VU cookie jar) and connection reuse | Accepted |
| [ADR-010](ADR-010-http2.md) | HTTP/2: `httpVersion` auto/1.1/2, h2c, `res.proto` | Accepted |
| [ADR-011](ADR-011-json-summary.md) | Versioned JSON summary (`--summary-json`) | Accepted |
| [ADR-012](ADR-012-html-report-and-time-series.md) | HTML report (`--report-html`) and per-second time series | Accepted |
| [ADR-013](ADR-013-discard-response-bodies.md) | Discard response bodies by default; `responseType` per request | Accepted |
| [ADR-014](ADR-014-protocol-modules.md) | Protocol modules: interface, explicit registration, run/VU/iteration lifecycle | Proposed |
| [ADR-015](ADR-015-metric-families.md) | Metric families (Trend, Counter, Rate) for protocols; reports and thresholds | Proposed |
| [ADR-016](ADR-016-error-normalization.md) | Normalized errors: `error` and `error_code` across protocols | Proposed |
| [ADR-017](ADR-017-async-model.md) | Asynchronous protocols: blocking calls and session-scoped loops, no global event loop | Proposed |

The numbering was reorganized on 2026-09-24 when the records moved from
`docs/adr/`. Each record lists its previous path.
