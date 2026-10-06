# Architecture decision records

Significant architecture decisions are recorded here as
`ADR-NNN-short-name.md`. New decisions take the next free number.

| ADR | Decision | Status |
|---|---|---|
| [ADR-001](ADR-001-script-runtime.md) | Script runtime: esbuild transpilation, one goja runtime per VU | Accepted |
| [ADR-002](ADR-002-cli-and-repository-layout.md) | CLI and repository layout | Accepted |
| [ADR-003](ADR-003-http-load-generator.md) | Minimal HTTP/1.1 load generator | Accepted (latency storage superseded by ADR-004) |
| [ADR-004](ADR-004-latency-histogram.md) | Fixed-size latency histogram | Accepted |
| [ADR-005](ADR-005-k6-shaped-script-api.md) | k6-shaped script API (Phase 1 DSL) | Accepted |
| [ADR-006](ADR-006-options-and-precedence.md) | Run options and their precedence | Accepted |
| [ADR-007](ADR-007-script-modules-and-globals.md) | Script modules and globals: built-ins, imports, `__ENV`, console, sleep | Accepted |
| [ADR-008](ADR-008-test-dsl.md) | Test DSL: responses, checks, thresholds, scenarios, setup/teardown | Accepted |
| [ADR-009](ADR-009-sessions-and-connection-reuse.md) | Sessions (per-VU cookie jar) and connection reuse | Accepted |

The numbering was reorganized on 2026-09-24 when the records moved from
`docs/adr/`. Each record lists its previous path.
