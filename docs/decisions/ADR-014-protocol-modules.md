# ADR-014: Protocol modules and their lifecycle

- Status: Accepted (2026-10-08, Phase 2). Refined by
  [ADR-018](ADR-018-protocol-architecture.md), which applies where the
  two differ.
- Date: 2026-10-08

## Context

Phase 2 adds WebSocket, gRPC, GraphQL and Kafka, and a test file must be
able to mix HTTP with at least one of them. Phase 1 has no protocol
abstraction. HTTP is wired into each layer:

| Layer | Coupling to HTTP |
|---|---|
| Module resolution | A hard-coded map of built-in module sources (`internal/script/modules.go`) |
| Import errors | The message names `"loadtool"` and `"loadtool/http"` (`script.go`) |
| VU | Holds the HTTP client and cookie jar |
| Runner | Creates one shared HTTP client and passes it to every VU, setup and teardown |

Two gaps matter for stateful protocols:

- **The engine has no VU-end hook.** HTTP needs none, because its
  connections live in a shared pool. A WebSocket connection, a gRPC
  connection or a Kafka client does need one.
- **There is no run-wide shared state.** Top-level script code runs once
  per VU, so anything set up there (a parsed `.proto` file, a Kafka
  client) would be built once per VU.

**Constraints from earlier decisions:**

- One goroutine and one goja runtime per VU; only `Runtime.Interrupt`
  crosses goroutines.
- Modules are built lazily, so a VU pays only for what its script uses
  (ADR-007's per-VU memory figures).
- No network calls in top-level code.
- Calls cut short by the end of the test are not recorded.
- No global mutable state (CLAUDE.md).

## Decision

### 1. A small interface package

`internal/protocol` defines the interfaces and imports no protocol:

```go
type Module interface {
    Name() string            // "ws" is imported as "loadtool/ws"
    Exports() []string       // named exports of the generated module source
    Metrics() []metrics.Def  // metric families it records (ADR-015)
    NewRun(RunEnv) (Run, error)
}

type Run interface {         // run-scoped: shared clients, caches
    NewVU(VU) (Instance, error)
    Close() error
}

type Instance interface {    // VU-scoped
    Object() goja.Value      // the module object, built lazily
    BeginIteration()
    Close() error
}

type VU interface {          // implemented by script.VU
    Runtime() *goja.Runtime
    Context() context.Context      // nil in top-level code
    Recorder() *metrics.Recorder   // nil in top-level code
    ID() int64
    Warn(msg string)
}
```

`RunEnv` carries what the runner already resolved: options, the shared
HTTP client (for GraphQL), TLS settings and the warning function.

### 2. Explicit registration, compiled in

`internal/runner` lists the modules:

```go
prog.WithModules(httpmod, ws, grpc, graphql, kafka)
```

There is no `init()` registration and no global registry. There are no
runtime or external plugins in Phase 2. The interface is the extension
point a later custom build could use.

### 3. The script package uses the registry for three things

- **Import resolution.** An unknown `loadtool/...` module lists the
  registered names.
- **Generated module sources.** The same `/* @__PURE__ */` re-export
  pattern as today, so esbuild still drops unused exports.
- **The lazy built-in object.** Modules are built on first access.

### 4. Only imported modules exist in a run

The bundle's import list says which modules a script uses. Only those
get a `Run`, VU instances and metric families. A script that imports
only `loadtool/http` costs exactly what it does today.

### 5. HTTP is registered, not rewritten

HTTP's existing code is wrapped as a module:

- `vu.request`, `httpclient.Do`, the cookie jar and the response object
  keep their code and their hot path;
- `BeginIteration` is where the cookie jar is reset today.

### 6. Lifecycle

| Scope | Created | Closed |
|---|---|---|
| Run | After options are resolved, before `setup` | After `teardown` |
| VU instance | With the VU, before the clock starts (as VUs are today) | When the engine stops the VU, after its last iteration |
| Iteration | `BeginIteration` at the start of `VU.Iterate` | — |
| Call | Inside a script call, bounded by `vu.Context()` | When it returns |

**Engine change (additive):** `NewVUFunc` also returns a close function,
which the engine calls once the VU has finished. Iterations still run
exactly as before.

**Shutdown order:**

1. VUs stop.
2. VU instances close.
3. Teardown runs, with its own instances.
4. Runs close.
5. Results are reported.

**Close rules:**

- `Close` must be idempotent and bounded in time.
- A close error becomes a warning, not a failed run.
- Ctrl+C behaviour is unchanged: one press stops the load and runs
  teardown; a second exits at once.

### 7. The lifecycle runtime gets instances too

The runtime that runs `setup` and `teardown` (VU 0) gets module
instances, so setup can use any protocol. As with HTTP today, its calls
are not counted in the results.

### 8. Module rules

- **No network calls in top-level code.** `vu.Context()` is nil there;
  modules throw the same kind of error HTTP throws.
- **Every blocking call selects on `vu.Context()`.** `Runtime.Interrupt`
  cannot reach code blocked in Go.
- **Calls cut short by the end of the test are not recorded.**
- **Per-VU state is built lazily, and run-wide data is shared
  read-only.** For example, parsed `.proto` descriptors are cached in the
  `Run`.

## Consequences

- **Adding a protocol means one package and one line in the runner.**
  Protocol code stays isolated (CLAUDE.md principle 12), and heavy
  dependencies stay inside their package.
- **HTTP-only behaviour, output and per-VU memory must not change.**
  Phase 2 exit criteria 2 and 3 check this.
- **The import error message and the troubleshooting page** must name
  the modules from the registry.
- **A fake test module exercises every hook,** with goroutine counts to
  catch leaks, before any real protocol is built on it.

## Alternatives considered

- **Global `init()` registration:** rejected; it is global mutable
  state, and tests could not choose modules.
- **Go `plugin` or out-of-process plugins:** rejected for Phase 2. Go
  plugins work only on Linux and break on version mismatches.
  Out-of-process plugins add a serialization cost to every call.
- **Moving HTTP fully onto the new interface:** rejected for now. It
  would risk the measured HTTP hot path for no user benefit.
