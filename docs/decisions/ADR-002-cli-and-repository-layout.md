# ADR-002: CLI and repository layout

- Status: Accepted
- Date: 2026-09-24
- Previously: `docs/adr/0001-cli-and-repository-layout.md` (ADR 0001)

## Context

Phase 0 needs a CLI entrypoint and a repository layout that separates CLI,
engine, runtime, protocol and metrics concerns without adding abstractions
before they are needed.

The early design notes (SKILL.md) named the executable `pt`; CLAUDE.md names
it `loadtool`.

## Decision

1. The executable is named **`loadtool`**, built from `cmd/loadtool`.
2. The CLI uses **Cobra** and lives in `internal/cli`. `cmd/loadtool/main.go`
   only calls `cli.NewRootCmd` and maps errors to exit code 1.
3. The root command is built by a factory (`NewRootCmd(stdout, stderr)`), not
   a package-level variable, so there is no global mutable command state and
   tests can capture output.
4. Commands that are not implemented yet return `cli.ErrNotImplemented` and
   a non-zero exit code, so automation never treats them as passing.
5. Packages such as `internal/engine`, `internal/runtime`, `internal/http`
   and `internal/metrics` are created only when their first feature is
   implemented. Empty placeholder packages are not added.
6. Everything is under `internal/` until an external API is deliberately
   designed; `pkg/` is not used yet.

## Consequences

- Engine code can be tested without going through Cobra.
- The public Go API surface is zero, so it can change freely during Phase 0.
- Documentation and examples use `loadtool`, not `pt`.
