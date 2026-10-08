# ADR-018: Phase 2 protocol architecture

- Status: Proposed (2026-10-08, Phase 2)
- Date: 2026-10-08
- Builds on: ADR-014 (protocol modules), ADR-015 (metric families),
  ADR-016 (error normalization), ADR-017 (async model)

## Context

Phase 2 adds WebSocket, gRPC, GraphQL and Kafka. One test file must be
able to mix HTTP with at least one of them. ADR-014 to ADR-017 set the
direction. This record turns them into one consistent design, written
against the Phase 1 code as it is, before any protocol is built.

**Where it differs from ADR-014, this record applies.** The differences
are listed under [Refinements](#refinements-to-adr-014).

**Requirements:**

1. Phase 1 HTTP keeps working: HTTP/1.1 and HTTP/2 through the existing
   `httpclient`.
2. WebSocket, gRPC, GraphQL and Kafka plug into common extension points.
3. Each protocol has its own package and can be tested on its own.
4. Protocol code contains no scenario logic and no command-line logic.
5. Protocols use the existing VU lifecycle.
6. Protocol errors map onto the existing result and metrics model.
7. Protocol-specific metrics stay available where they mean something.

**Out of scope:**

- distributed execution, remote workers and plugins loaded at run time;
- the protocol modules themselves, each of which gets its own ADR
  (019–022).

### Phase 1 facts this design relies on

| Fact | Where |
|---|---|
| One goja runtime per VU. It is used by one goroutine at a time; only `Runtime.Interrupt` crosses goroutines. | `internal/script/script.go` |
| VUs are created one at a time, before the clock starts. Each VU then runs on **one goroutine** for its whole scenario, and `RunScenarios` waits for all of them. | `internal/engine/scenario.go`, `executors.go` |
| The engine sees only `IterationFunc(ctx, *metrics.Recorder)`. | `internal/engine/engine.go` |
| Built-in modules are lazy objects, so a VU pays only for what it uses (about 1–2 KB for a small HTTP script, ADR-007). | `internal/script/lazy.go` |
| One run-wide HTTP client holds the connection pool. Each VU wraps it with its own cookie jar (`httpclient.WithJar`), and the jar is reset each iteration unless `noCookiesReset` is set. | `runner.go`, `httpclient/jar.go` |
| Setup and teardown run in VU 0 and record into a throwaway recorder, so they are never counted. | `internal/script/lifecycle.go` |
| A call cut short by the end of the test is not recorded. | `httpclient.Do` |
| `report.Verdict` decides the exit code and the JSON `outcome`. | `internal/report/outcome.go` |

## Decision

### 1. Layers and packages

```
cmd/loadtool ─► internal/cli ─► internal/runner ──────► internal/engine (unchanged)
                                   │  module list            │
                                   ▼                         ▼
                              internal/script ──► internal/protocol (interfaces, errors, recording helper)
                                   │                         ▲
                                   │ HTTP adapter            │ implement
                                   ▼                         │
                              internal/httpclient     internal/protocols/{ws,grpc,graphql,kafka}
                                                              │
                                                       internal/metrics (families, ADR-015)
```

| Package | Contains | Must not import |
|---|---|---|
| `internal/protocol` | The interfaces (section 2), `ErrorCode` and `Classify`, the `Outcome` recording helper, the `protocoltest` harness (sub-package) | Any protocol, `script`, `engine`, `runner`, `config`, `cli`, `report`, `thresholds` |
| `internal/protocols/<name>` | One protocol: a **transport** layer in plain Go (no goja) and a **binding** layer (the module object and results) | `script`, `engine`, `runner`, `config`, `cli`, `report`, `thresholds`, other protocols |
| `internal/script` | The registry wiring, generated module sources, the `protocol.VU` implementation, and the **HTTP adapter** over the existing Phase 1 HTTP code | Any protocol package |
| `internal/runner` | The **only** place that lists modules, and the owner of `Run` and `Instance` lifetimes | — |

**Allowed imports for a protocol package:**

- `internal/protocol`;
- `internal/metrics`;
- `internal/httpclient`, for protocols carried over HTTP (GraphQL);
- the standard library, `goja`, and its own third-party dependency.

**An import test enforces these rules,** as the benchmark server's
`TestImportsStandardLibraryOnly` already does.

**This meets requirements 3, 4 and 5:**

- Scenario logic stays in `engine` and `config`.
- Command-line logic stays in `cli`.
- Protocol packages can't reach either.

### 2. Interface design

```go
package protocol

// Module is a protocol, compiled in and listed by the runner.
type Module interface {
    Name() string            // "ws": imported as "loadtool/ws"
    Exports() []string       // named exports of the module ("connect", ...)
    Metrics() []metrics.Def  // metric families (ADR-015)
    NewRun(RunEnv) (Run, error)
}

// RunEnv is what a module gets from the run. It carries no scenario
// or command-line state.
type RunEnv struct {
    Warn func(msg string)  // one warning per message per run
    TLS  *tls.Config       // nil: system defaults (tests set it)
    Options json.RawMessage // options.<Name()>, or nil (see section 11)
    // MaxVUs is informational, for sizing pools, as
    // httpclient.MaxConnsPerHost is sized today.
    MaxVUs int
}

// Run is the module's state for one test run, shared by its VUs.
type Run interface {
    NewInstance(VU) (Instance, error)
    Close(ctx context.Context) error
}

// Instance is the module's state in one VU's runtime.
type Instance interface {
    Value() goja.Value  // the module object, built lazily
    BeginIteration()    // reset per-iteration state
    Close(ctx context.Context) error
}

// VU is what a module may use from the VU it belongs to.
// script.VU implements it.
type VU interface {
    Runtime() *goja.Runtime
    ID() int64                   // __VU; 0 for the setup/teardown runtime
    Context() context.Context    // nil in top-level code
    Recorder() *metrics.Recorder // nil in top-level code; a throwaway one in setup and teardown
    HTTPClient() *http.Client    // this VU's HTTP session: shared pool, own cookie jar
    Warn(msg string)
    // OnClose registers a resource the script created (a gRPC client,
    // say). It is closed with this VU's instances (section 4, step 7).
    OnClose(io.Closer)
}
```

**Rules for the interfaces:**

- **Small.** Four interfaces and one struct. Each method has a caller in
  Phase 2.
- **Built only when imported.** A module gets a `Run` only if the
  bundled script imports it; esbuild reports the imports. Its `Instance`
  is built on the first access in a VU, like today's lazy modules. An
  HTTP-only script therefore builds exactly what it builds today.

### 3. HTTP through the existing implementation

HTTP is adapted, not moved or rewritten:

- **`internal/script/httpmodule.go` implements `Module`, `Run` and
  `Instance`** around the existing `vu.request`, `response`, cookie jar
  and `httpclient.Do`.
- **`BeginIteration` resets the jar,** as `VU.Iterate` does today.
- **HTTP/1.1 and HTTP/2** stay in `httpclient` and are selected by
  `options.httpVersion` (ADR-010), as now. Protocols carried over HTTP
  reach that client only through `VU.HTTPClient()`, so they inherit
  `httpVersion`, connection reuse and the cookie jar.
- **HTTP keeps its dedicated metrics.** `Recorder.Record`,
  `RecordUnsent` and `RecordProtocol` are unchanged, and so are the
  `http_*` thresholds and report fields (section 8).
- **Moving the adapter** to `internal/protocols/http` is possible later.
  It is not part of Phase 2.

### 4. Lifecycle

| Step | Who | Phase 1 point it attaches to |
|---|---|---|
| 1. `NewRun` for each imported module | runner | After options are resolved, before `setup` |
| 2. `NewInstance` in the lifecycle runtime (VU 0), lazily | script | First access in setup/teardown code |
| 3. `setup()` | script | Unchanged |
| 4. `NewInstance` per VU, lazily | script | First access, at the earliest in the VU's top-level code (no network allowed there) |
| 5. `BeginIteration` on every instance the VU has built | script | Start of `VU.Iterate`, where the jar is reset now |
| 6. Calls | module, on the VU's goroutine | Inside iterations |
| 7. `Close` on every VU instance | runner | After `engine.RunScenarios` returns, so every VU goroutine has ended. **No engine change.** |
| 8. `teardown()`, then `Close` on the lifecycle instances | script, runner | Teardown unchanged |
| 9. `Run.Close` | runner | After teardown, before results are returned |

**Closing:**

- **Each close is bounded** by a context deadline (5 s per step).
- **A close error becomes a warning;** it does not fail the run.
- **A second Ctrl+C** exits at once, as now, with no cleanup.
- **Instances are closed only after the whole run.** A VU that a
  ramp-down retires early keeps its connections until then. This trades
  a little held capacity for leaving the engine untouched.

### 5. VU ownership

- **An instance belongs to exactly one VU,** and is used only on the
  goroutine running that VU: the runner's goroutine while the VU is
  created, then the VU's own goroutine. The engine's `go` statement
  orders the hand-over, as it does for the goja runtime today.
- **Live objects never cross VUs.** Setup data is passed as JSON
  (ADR-008), so a connection made in setup cannot reach a VU. Each VU
  connects for itself.
- **The setup/teardown runtime has its own instances.** They are closed
  after teardown, and their recordings are thrown away.

### 6. Connection and session ownership

| Protocol | Connection or client | Owner | Session state | Owner and reset |
|---|---|---|---|---|
| HTTP/1.1, HTTP/2 | Pooled connections in the shared transport | Run (runner, as now) | Cookie jar | VU; reset each iteration unless `noCookiesReset` |
| GraphQL | The VU's HTTP session (`VU.HTTPClient()`) | Shared with HTTP | The HTTP cookie jar | As HTTP |
| WebSocket | One connection per `ws.connect` call | The call: closed before `connect` returns | Callbacks, timers, buffered frames | The call |
| gRPC | One connection per client object that the script connects (scope decision 3: per VU) | The VU, via `VU.OnClose`; it persists across iterations until `client.close()` | Metadata the script sets per call | The call |
| gRPC descriptors | Parsed `.proto` files | Run: read-only, shared by VUs | — | — |
| Kafka | One client per broker configuration (scope decision 4: per run) | Run; goroutine-safe client | Consumer position, where per-VU | VU instance |

**Rules behind the table:**

- **Heavy, goroutine-safe clients are run-owned.** Per-user state is
  VU-owned. Anything tied to one exchange is call-owned.
- **The run owns a resource only if the third-party library documents
  it as safe for concurrent use.**
- **Each protocol ADR (019–022) confirms its row.** The gRPC row is
  checked by measurement at 1,000 VUs.

### 7. Request/response model

**Calls block,** as `http.get` does (ADR-017):

- they run on the VU's goroutine;
- they return a **result object**;
- WebSocket's `connect` blocks for the whole session and dispatches
  callbacks on the VU's goroutine.

**Result objects:**

- **Lazy.** They are goja dynamic objects whose fields are built on first
  read, as `response` is today.
- **Common fields** on every result:

  | Field | Value |
  |---|---|
  | `error` | `""` or a message |
  | `error_code` | `""` or a category (section 8) |
  | `timings.duration` | Milliseconds, where a single duration applies |

- **Protocol fields** come from each protocol ADR. Examples: `status` for
  HTTP and gRPC; `message` for gRPC; `data` and `errors` for GraphQL;
  `offset` for Kafka.

**Payloads follow ADR-013:**

- Large payloads are kept only when the script asks for them.
- WebSocket messages are delivered to callbacks, not stored.
- Each protocol ADR states its default.

**Failures and misuse:**

| Situation | Handling |
|---|---|
| A network, protocol or server failure | Returned in the result (ADR-016) |
| Misuse: bad arguments, a network call in top-level code | Throws a `TypeError` or `GoError`, counted as a script error |

### 8. Error and metrics model

**Every operation that can succeed or fail goes through one helper,** so
the rules are written once:

```go
// Outcome is one finished operation.
type Outcome struct {
    Duration time.Duration
    Err      error     // nil on success
    Code     ErrorCode // ""; or set by the module for server errors
    Sent     bool      // false: never left LoadTool (an invalid request)
}

// Record records out in the given families, on the VU's goroutine.
// It records nothing if the VU's context has ended (the test stopped).
// If out.Code is empty and Err is set, it classifies Err (Classify).
// It returns the final code for the result's error_code field.
func Record(vu VU, f Families, out Outcome) ErrorCode
```

`Families` names the family IDs an operation records: its Trend, its
Counter and its failure Rate, any of which may be absent.

**Errors (ADR-016):**

- **Codes:** `dns`, `dial`, `tls`, `timeout`, `protocol`, `server`,
  `closed`, `invalid`.
- **Transport errors** are classified by `Classify`.
- **Application errors** (gRPC status, GraphQL `errors`, Kafka broker
  errors, abnormal WebSocket closes) are coded `server` by the module,
  which keeps the native detail in its own fields.
- **Failed means a non-empty code,** so `<protocol>_req_failed` and
  `error_code` always agree.
- **The end of the test is not an error.** Cancellation is detected
  through the context, as `httpclient.Do` does, and nothing is recorded.

**What gets recorded:**

| Outcome | Trend | Counter | Failure rate |
|---|---|---|---|
| Sent, succeeded | `ok` sample | +1 | pass |
| Sent, failed | `failed` sample | +1 | fail |
| Not sent (`invalid`) | none: no latency, like `RecordUnsent` | +1 | fail |
| The test ended | nothing | nothing | nothing |

**Metrics (ADR-015):**

- **Each module declares families** (Trend, Counter, Rate). They are
  allocated only for imported modules, and recorded with integer IDs: no
  maps and no allocations on the hot path.
- **Protocol-specific metrics stay available** as their own families:
  `ws_msgs_sent` and `ws_msgs_received`, `ws_connecting`,
  `kafka_messages_consumed` and so on.
- **They appear in thresholds and in the console, JSON and HTML reports
  only when used.** HTTP-only output stays byte-identical.
- **Only the VU's goroutine records into a VU's `Recorder`.** Background
  goroutines, such as a WebSocket reader, hand data to the VU's
  goroutine, which records it. So `Recorder` stays lock-free, as it is
  today.
- **`checks` and `iterations` stay global** across protocols.
  `report.Verdict` does not change: thresholds on families fail the run
  (exit 99) exactly as `http_*` thresholds do.

**HTTP gets `error_code` too.** The adapter derives it with the same
`Classify`: status ≥ 400 gives `server`, and an unsent request gives
`invalid`. HTTP's metrics recording is unchanged.

### 9. Cleanup

Cleanup happens at three scopes:

- **Call scope:** a call releases everything it opened before returning,
  including on errors and cancellation. A WebSocket session closes its
  socket and **waits for its reader goroutine to exit.**
- **VU scope:** Go resources a script creates and keeps (a gRPC client)
  are registered with `VU.OnClose`. They are closed with the VU's
  instances (section 4, step 7), after the instances' own `Close`. A script's `close()` releases them early and makes
  the later close a no-op. Garbage collection of the JavaScript object
  never closes anything.
- **Run scope:** `Run.Close` releases shared clients and caches.

**Requirements on every close:**

- idempotent;
- bounded by its context;
- safe to call on a VU that never iterated.

**Leak tests:** every protocol's tests assert that the number of
goroutines returns to its starting value after `Close`.

### 10. Concurrency model

1. **JavaScript runs only on the VU's goroutine.** Module code called
   from JavaScript runs there too.
2. **Module goroutines are allowed only in two places:**
   - **inside a call, joined before the call returns** (the WebSocket
     reader);
   - **owned by a `Run` or `Instance` and stopped by its `Close`** (a
     Kafka client's internal goroutines).

   They never touch goja or a `Recorder`. They talk to the VU's
   goroutine through channels, bounded where messages arrive from the
   network.
3. **Run-scoped state is either immutable after `NewRun`** (descriptors)
   **or safe for concurrent use** (the Kafka client, the HTTP
   transport).
4. **Every blocking call selects on `VU.Context()`.**
   `Runtime.Interrupt` cannot reach Go code. When the test stops, the
   call returns quickly and records nothing.
5. **The race detector runs on every protocol's tests,** as CI already
   does.

### 11. Extensibility

**Adding a protocol takes:**

1. a package under `internal/protocols/<name>` implementing `Module`,
   with a transport layer and a binding layer;
2. one line in the runner's module list;
3. an ADR for its API, ownership row, failure rule, metric names and
   payload default;
4. tests using `protocoltest`, plus transport tests;
5. a demo target, an example, docs and a benchmark.

Nothing in `engine`, `config` scenarios, `cli`, `thresholds` or `report`
changes per protocol. Thresholds and reports pick up the families
through `metrics.Def`.

**Options:**

- **A module's options live under `options.<name>`**, for example
  `options.grpc`.
- **Config treats registered module names as known keys,** so they
  produce no "unsupported option" warning.
- **The module receives the raw JSON** in `RunEnv.Options` and parses it
  in `NewRun`.
- **A module with no options** ignores the field.

**`protocoltest` lets each package test itself.** It provides:

- a goja runtime;
- a `VU` implementation with a context, a recorder and an HTTP client;
- `Run(js string)`, to run script code against the module;
- helpers to read the recorded families.

No `script`, `runner` or `engine` is involved, so a protocol package
tests itself without the rest of LoadTool.

## Refinements to ADR-014

| ADR-014 said | This record | Why |
|---|---|---|
| The engine calls a VU close hook | The runner closes instances after `RunScenarios` returns | `RunScenarios` already waits for every VU goroutine, so no engine change is needed |
| `Instance.Object()`, `Close() error` | `Value()`, `Close(ctx) error` | Closing must be bounded |
| `VU` without HTTP access | `VU.HTTPClient()`, `VU.OnClose` | GraphQL needs the VU's HTTP session; resources a script creates need an owner |
| `NewVU(VU)` on `Run` | `NewInstance(VU)`, built lazily on first access | Matches the existing lazy modules, so unused modules cost nothing per VU |

## Consistency check

| Requirement | Met by |
|---|---|
| 1. Phase 1 HTTP preserved | §3 HTTP adapter; §8 HTTP metrics unchanged; exit criteria 2 and 3 |
| 2. HTTP/1.1 and HTTP/2 through the existing code | §3: `httpclient` and `httpVersion` unchanged; GraphQL goes through `VU.HTTPClient()` |
| 3. Extension points for four protocols | §2 interfaces; §11 |
| 4. Independently testable | §1 transport/binding split; §11 `protocoltest` |
| 5. Own package each | §1 `internal/protocols/<name>` |
| 6. No scenario logic | §1 import rules; `RunEnv` carries no scenario state |
| 7. No command-line logic | §1 import rules |
| 8. Uses the VU lifecycle | §4 attaches to `NewVUExec`, `Iterate`, setup/teardown, the end of `RunScenarios` |
| 9. Errors normalized | §8 `Outcome`, `Record`, `ErrorCode` |
| 10. Protocol metrics available | §8 families, thresholds, reports |

**Checked against the code:**

- **The setup throwaway recorder:** §5 and §8 need no special case.
- **Each VU's single goroutine:** §5 and §10.
- **`RunScenarios` waits for every VU:** §4 step 7.
- **Lazy modules:** §2.
- **Cancellation is not recorded:** §8.

## Consequences

- **The engine, scenarios and executors do not change.** The runner and
  `script` gain the wiring, and `metrics`, `thresholds` and `report` gain
  families (ADR-015).
- **New interfaces have no implementation until the first protocol
  lands.** Step 2 of the Phase 2 plan therefore builds a fake test module
  that exercises every hook, including leak checks, before
  GraphQL and WebSocket use it.
- **Instances are closed only at the end of the run** (§4), so resources
  are held a little longer.
- **HTTP's adapter lives in `script`** rather than in its own protocol
  package. This is a deliberate exception that keeps the Phase 1 HTTP
  code where it is.

## Alternatives considered

- **A per-VU close hook in the engine:** not needed (see Refinements).
- **One generic `Record(name string, …)` API:** rejected. A map lookup on
  every operation would sit in the hot path.
- **Recording from background goroutines with a locked `Recorder`:**
  rejected. It adds a lock to every HTTP request for the sake of a few
  protocols.
- **Moving HTTP into `internal/protocols/http` now:** rejected. It is a
  large refactor of measured code, with no user benefit in Phase 2.
- **Run-time plugins and a distributed or remote architecture:** out of
  scope (scope decision 5).
