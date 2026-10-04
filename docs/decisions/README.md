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

The numbering was reorganized on 2026-09-24 when the records moved from
`docs/adr/`. Each record lists its previous path.
