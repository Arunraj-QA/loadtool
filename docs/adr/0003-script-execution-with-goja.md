# ADR 0003: Script execution with goja

- Status: Accepted
- Date: 2026-09-24

## Context

Users write tests in TypeScript. Each VU must call the script's
`export default function` once per iteration. goja runs JavaScript (ES5.1
and most of ES2015+), but it does not understand TypeScript syntax or ES
module `export` statements. A `goja.Runtime` is not goroutine-safe.

## Decision

1. **Transpile with esbuild's Go API** (`github.com/evanw/esbuild/pkg/api`).
   - It strips TypeScript types, converts ES module exports to CommonJS,
     targets ES2017, and emits an inline source map.
   - goja reads that source map, so errors point at the original `.ts`
     line.
   - esbuild is pure Go (no cgo, no Node.js).
   - Types are **not** checked; that needs the TypeScript compiler, which
     runs on Node.js.
2. **Compile once and share.** The transpiled code is compiled once into a
   `*goja.Program`. goja documents compiled programs as safe for concurrent
   use, so all VUs share it.
3. **One `goja.Runtime` per VU**, created and used only by that VU's
   goroutine.
   - No JavaScript values or globals are shared between VUs; module-level
     `let` state stays inside each VU.
   - The only Go state shared by VUs is the compiled program and the
     `*http.Client`, which is safe for concurrent use and lets VUs share one
     connection pool.
4. **VUs are created before the clock starts.**
   - `engine.Run` takes a `NewVUFunc` and calls it for every VU,
     sequentially, before the test duration begins.
   - Each call runs the script's top-level (init) code and resolves the
     default export.
   - Any init error aborts the run before load is generated.
5. **Iteration.**
   - `VU.Iterate` stores the iteration's context and recorder on the VU,
     then calls the default function.
   - Go callbacks (the `http` API) run on the VU's own goroutine, so they
     read that state without locks.
6. **Stopping scripts at the end of the test.**
   - Each iteration registers `context.AfterFunc(ctx, rt.Interrupt)`. That
     uses no goroutine until the context ends, and `Interrupt` is the one
     goroutine-safe runtime method.
   - Scripts stuck in loops therefore stop at the end of the test.
   - An interrupt at the end of the test is not counted as an error.
7. **Error handling.**
   - Exceptions thrown during an iteration end that iteration only. They
     are counted as script errors (`metrics.Recorder.RecordScriptError`),
     and the first message is shown in the summary.
   - HTTP transport failures return `status: 0` with an `error` message
     instead of throwing.
   - HTTP calls in top-level code throw: they would run outside the
     measured window.
8. **Minimal API:** `http.get` and `http.request`, returning
   `{ status, error, timings: { duration } }`.
   - Response bodies are not exposed, because turning every body into a
     JavaScript string costs memory on every request. That can be added
     later as an opt-in.
9. The `--url` flag is removed: the script decides what to request.

## Alternatives considered

- **Require plain JavaScript only.** This avoids the esbuild dependency but
  drops the TypeScript authoring that the project is built around.
- **Strip `export default` with string rewriting.** This is fragile and
  still cannot remove TypeScript type syntax.
- **One shared runtime behind a mutex.** This serializes every VU and makes
  JavaScript state shared by default. Rejected.
- **A pool of runtimes.** This saves memory, but VUs would lose their own
  state between iterations. It can be revisited if per-VU memory becomes
  the bottleneck.

## Consequences

- Binary size grows to about 28 MB (esbuild and goja).
- Each VU allocates about 40 KB while it is created (`BenchmarkNewVU`,
  458 allocations) and keeps about 30 KB (`BenchmarkVURetainedMemory`).
  About 22 KB of that comes from esbuild's CommonJS interop helpers, which
  every runtime executes; see `benchmarks/2026-09-24-vu-memory.md`.
- Calling the default function costs about 370 ns and 4 allocations per
  iteration before any request (`BenchmarkIterateEmpty`).
- `async` default functions are not supported: there is no event loop, and
  the `http` API is synchronous.
