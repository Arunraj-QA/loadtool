# ADR-007: Script modules and globals

- Status: Accepted
- Date: 2026-10-04

## Context

ADR-005 chose a k6-shaped script API. Step 4 of Phase 1 adds the core of
that API:

- built-in modules
- relative imports
- the `__ENV`, `__VU` and `__ITER` globals
- `console`
- `sleep` and `group`

Each VU has its own goja runtime (ADR-001). Every object a script can
reach is therefore built once per VU, so at 1,000 VUs each 1 KB per VU
costs 1 MB. Per-VU memory is the Phase 0 exit criterion and a Phase 1
gate, so these decisions are about API shape and memory together.

## Decision

1. **Built-in modules are virtual ES modules.** The esbuild plugin serves
   `loadtool/http` and `loadtool`. Each re-exports Go objects from the
   hidden global `__loadtool_builtin`, for example:

   ```js
   const m = globalThis.__loadtool_builtin.http;
   export default m;
   export const get = m.get;
   ```

   - esbuild binds imports directly, so bundles contain no interop
     helpers.
   - Unused exports, such as `group`, are removed by tree shaking.
   - `group` is written in JavaScript in the module source. It needs no Go.
   - Import paths are `loadtool/...`. There are no `k6/...` aliases
     (ADR-005).
2. **Imports are built-ins or relative paths only.**
   - `./helpers.ts` and `../lib/x.js` are bundled by esbuild, resolved
     from the script's directory.
   - Bare package names (`lodash`) and unknown `loadtool/...` modules are
     errors that name the modules that exist. Package resolution needs a
     dependency story, which is not part of Phase 1.
3. **Built-ins are built lazily, on first access.**
   - `__loadtool_builtin` and `console` are `lazyObject`s, which are goja
     dynamic objects with a fixed property list.
   - A module's Go object is built only when the bundled module code first
     reads it, which happens only if the script imports it.
   - A console method is built when it is first used.
   - Measured: one function object costs about 750 B per runtime, whether
     it is native or JavaScript. Building every built-in eagerly cost
     7 KB per VU.
   - Lazy properties can be replaced (`console.log = ...`) but not added
     or deleted.
4. **Bundles use esbuild's IIFE format.**
   - Top-level declarations stay scoped to the script, as in an ES module,
     instead of becoming properties of the global object.
   - This matches ES module semantics, which k6 follows too, and saves
     1.3–2 KB per VU.
   - The entry still publishes the default export and `options` through
     `globalThis.__loadtool_default` and `globalThis.__loadtool_options`.
5. **`__ENV`** is the process environment plus `--env KEY=VALUE` flags,
   with the flags taking precedence.
   - It is a dynamic object over one map shared by all VUs, which is
     read-only during the run.
   - Each VU's writes and deletes go into its own overlay, allocated on
     first write. VUs never see each other's changes.
   - `options` can read `__ENV`.
6. **`__VU`** is 1..N in VUs and 0 in the runtime that reads `options`.
   **`__ITER`** is the VU's iteration number, starting at 0.
7. **`console`** has `log`, `info`, `warn`, `error` and `debug`.
   - Output goes to stderr, so stdout keeps only the report.
   - Each call writes one whole line, formatted as `LEVEL [VU n] args`.
   - Objects are printed as JSON.
   - All VUs share one lock, so lines never interleave.
8. **`sleep(seconds)`** is implemented in Go.
   - It returns early when the run ends or is cancelled, so it never
     delays shutdown.
   - It is rejected in top-level code.
   - Invalid arguments throw a `TypeError`.
9. **`group(name, fn)`** runs `fn` and returns its result. Tagging metrics
   with the group name waits for the metrics registry (checks and
   thresholds steps).

## Consequences

- Retained heap per VU, measured with `BenchmarkVURetainedMemory`:

  | Script | Before step 4 | After step 4 |
  |---|---|---|
  | No imports | 5,984 B | 4,608 B |
  | Imports `loadtool/http` | 5,984 B | 6,568 B |
  | Imports `loadtool/http` and `loadtool` | — | 7,704 B |

  - Before step 4, every VU built the `http` global whether or not the
    script used it, so the one figure covers both of its rows.
  - A script that uses http now costs 584 B more per VU (+10%). That
    covers `__ENV`, `__VU`, `__ITER`, `console` and the module wrapper.
  - Process-level results are in
    `benchmarks/results/2026-10-04-dsl-core-memory.md`.
- Each new built-in module or global adds to every VU unless it is lazy.
  New built-ins should be added to a `lazyObject` and measured with the
  retained-memory benchmark.
- Script variables are no longer reachable as `globalThis.x`. Tests that
  need them assign them to `globalThis` explicitly.
- Named imports from a module read all of its exports when the script
  starts. For example, importing `loadtool/http` builds both `get` and
  `request`.
