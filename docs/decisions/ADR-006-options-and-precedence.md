# ADR-006: Run options and their precedence

- Status: Accepted
- Date: 2026-10-04

## Context

Phase 0 took its settings only from CLI flags. Phase 1 follows the shape
of k6's API (ADR-005), where a script declares its settings:

```typescript
export const options = { vus: 10, duration: "30s" };
```

The same setting can then come from the script, the command line or the
environment. The order needs to be fixed before scenarios and thresholds
add many more options.

## Decision

1. **Precedence, highest first:**
   1. CLI flag, if given on the command line (`--vus`, `--duration`)
   2. Environment variable (`LOADTOOL_VUS`, `LOADTOOL_DURATION`)
   3. Script `options` (`export const options = {...}`)
   4. Built-in default (1 VU, 10 s)

   This is the same order k6 documents. A flag left at its default does
   not override the script; only flags actually typed count.
2. **Reading script options.**
   - The generated bundle entry assigns `mod.options` to a global next to
     the default export.
   - esbuild turns that into a direct reference, so no interop helpers
     (and no per-VU memory) are added. A script without `options` gets
     `undefined`, not an error.
   - LoadTool runs the script's top-level code once in a separate
     runtime, converts `options` to JSON inside goja and decodes it into a
     typed Go struct.
3. **Values.** Keys and types follow k6.
   - `vus` is a whole number.
   - `duration` is a Go-style duration string (`"30s"`, `"1m30s"`) or a
     number of milliseconds.
   - Environment variables use the same string forms.
4. **Unknown keys produce a warning, not an error.** A script written for
   k6 that sets options LoadTool does not support yet still runs, and the
   warning says which keys were ignored.
5. **Errors name their source**, e.g.
   `vus must be at least 1 (from script options)`.
6. **Scope.** Only the settings that exist today (`vus`, `duration`) are
   read in this step.
   - Scenarios, thresholds and HTTP options extend the same struct in
     their own steps.
   - `--graceful-stop` stays a flag. In k6 it is a per-scenario setting,
     and it will move there with scenarios.

## Consequences

- One resolved `config.Config` feeds the engine, whatever the source.
  Callers don't need to know where a value came from, except for error
  messages.
- **Every run executes the script's top-level code once more**, in the
  options runtime. That is cheap (about 5 µs and 6 KB per runtime,
  measured for VU start-up), but it means top-level side effects such as
  `console.log` run one extra time, as in k6.
- **Defaults differ from k6:** without `duration`, LoadTool runs for 10 s,
  while k6 runs a single iteration. This is a documented difference until
  iteration-count executors exist.
