# ADR-019: WebSocket module

- Status: Accepted (2026-10-09, Phase 2), with the amendment for the
  blocking style
- Date: 2026-10-08
- Builds on: ADR-017 (async model), ADR-018 (protocol architecture)

## Context

Phase 2's first protocol module. The roadmap asks for:

- connect, send, receive and close;
- latency per message;
- connection metrics, and send and receive errors;
- clean cancellation and failure handling;
- integration with checks, thresholds and the JSON and HTML reports;
- a test file that mixes HTTP and WebSocket.

ADR-017 decided the shape: a blocking `connect` with a loop scoped to
the session.

## Decision

### API (`loadtool/ws`, the shape of k6's `k6/ws`)

```ts
const res = ws.connect(url, params?, (socket) => {
  socket.on("open", () => socket.send("hi", { reply: true }));
  socket.on("message", (data) => { /* string, or ArrayBuffer for binary */ });
  socket.on("close", (code) => {});
  socket.on("error", (e) => {});          // { error, error_code }
  socket.setTimeout(() => socket.close(), 5000);
});
// res: { status, error, error_code, url, timings: { connecting, duration } }
```

| Function | Behaviour |
|---|---|
| `connect(url, params?, setup)` | Dials (`ws://` or `wss://`), runs `setup(socket)`, fires `open`, then **blocks until the socket closes**. `params.headers` go on the handshake, and the **VU's cookie jar** is used, so a login over HTTP carries over. |
| `socket.send(text, { reply })`, `socket.sendBinary(buffer, { reply })` | Writes one message (30 s write timeout) and returns whether it was written |
| `socket.close(code = 1000)` | Starts the close handshake |
| `socket.setTimeout(fn, ms)`, `socket.setInterval(fn, ms)` | Timers that run inside the session |

### Session model (ADR-017)

**One session per `connect` call.** The connection, the reader
goroutine, the handlers and the timers belong to that call, and are
released before it returns. A VU therefore holds at most one session,
and no WebSocket state is shared between VUs. The run shares only:

- the HTTP/1.1 handshake transport, which is safe for concurrent use;
- the metric family IDs, which never change.

**The reader goroutine:**

- reads frames, **stamps their arrival time**, and passes them on a
  bounded channel (16) to the VU's goroutine;
- never touches goja or the recorder.

**The VU's goroutine runs the loop:** it dispatches events, runs timers
and records metrics.

**Closing:**

- **A script's close runs on a goroutine of its own,** because the
  library's close handshake waits up to 5 s to write and 5 s for the
  peer's close frame, which the reader must stay free to deliver. That
  goroutine is joined before `connect` returns.
- **Before `connect` returns,** it stops the reader, closes the
  connection at once (`CloseNow`), drains the channel until the reader
  exits, and waits for the close goroutine. **No goroutine outlives the
  call.**
- **When the test ends,** the context is cancelled: the loop exits, the
  connection is closed at once, and nothing about the session is
  recorded.
- **A handler that throws** ends the session (the socket is closed), and
  `connect` rethrows, so the iteration ends with a script error.

### Latency per message

`send(…, { reply: true })` records the send time. The next message
received is taken as the reply, matched first in, first out, and
`ws_msg_latency` records arrival − send.

- **The arrival time is taken in the reader goroutine,** so time spent
  in handlers does not inflate it.
- **Only marked sends are timed,** so servers that push unsolicited
  messages do not produce false samples.
- **This assumes in-order replies,** as echo and request/reply protocols
  give. Pipelined sends therefore include server-side queueing: five
  sends to an echo server that takes 5 ms each give about 5, 10, 15, 20
  and 25 ms.
- **The queue is bounded:** at most 1,024 sends can await a reply per
  session; past that, the oldest stops being timed, with a warning.

### Metrics (ADR-015)

| Family | Kind | Meaning |
|---|---|---|
| `ws_connecting` | Trend | Handshake time; failed handshakes are failed samples |
| `ws_sessions` | Counter | Connection attempts |
| `ws_session_failed` | Rate | True when the handshake failed or the session ended abnormally |
| `ws_session_duration` | Trend | From the end of the handshake to the end of the session |
| `ws_msgs_sent`, `ws_msgs_received` | Counter | Data messages |
| `ws_msg_latency` | Trend | Reply latency (see above) |
| `ws_errors` | Counter | Send and receive errors during sessions |

**Abnormal endings:**

- an error;
- a close code other than 1000 (normal) or 1001 (going away);
- the connection dropping (1006).

### Errors (ADR-016)

**Handshake failures** are returned in `res` and the callback is not
called:

| `error_code` | When |
|---|---|
| `dial`, `dns`, `tls`, `timeout` | Classified from the transport error |
| `server` | The server refused the upgrade with HTTP ≥ 400 |
| `protocol` | Any other bad handshake |
| `invalid` | A URL that is not `ws://` or `wss://` (never sent) |

**During the session:**

- **A failed send or receive** fires `error` (`{ error, error_code }`),
  counts in `ws_errors` and ends the session.
- **A dropped connection** is `closed`.
- **A close code other than 1000 or 1001** is `server`.

**Misuse throws:**

- `connect` in top-level code;
- a missing URL or callback;
- an unknown event name;
- a non-function handler.

### Limits

- **Messages:** at most 1 MiB each; a larger one is a receive error.
- **Handshake:** at most 30 s.
- **Not supported yet:** subprotocols, compression, `ping`/`pong` events.

## Consequences

- **Mixed tests work.** HTTP and WebSocket run in one iteration and are
  measured apart; thresholds can name `ws_*` families. The runner test
  and `examples/websocket.ts` show this.
- **Proof of the architecture.** It is the first module built on
  ADR-018: the runner closes VU instances after the run and runs after
  teardown. Its tests check goroutine counts after normal sessions,
  throwing handlers and cancellation.
- **New dependency:** `github.com/coder/websocket` (ISC licence), used
  by `internal/protocols/ws` and the demo API's echo endpoint.

## Alternatives considered

- **Per-message latency for every send, matched first in, first out:**
  rejected. Servers that push messages would produce wrong samples
  silently.
- **Request–reply correlation by message ID:** not added in this step.
  It needs a script-provided matcher and is not needed for the roadmap's
  echo-style latency.
- **Running the close handshake on the VU's goroutine:** rejected. It
  could deadlock with a full event channel until the 5 s timeout.
- **`gorilla/websocket`:** usable, but `coder/websocket` takes contexts,
  which cancellation needs.

## Amendment (2026-10-09): the blocking style

Users asked for a request/reply style:

```ts
const socket = ws.connect(url);
socket.send("hello");
const reply = socket.receive(5000);
socket.close();
```

It fits the blocking VU model (ADR-017), which has no event loop, so it
is added next to the callback style rather than replacing it.

### API

**`ws.connect(url, params?)` without a setup function returns a socket
at once.** Its fields are the result's (`status`, `error`, `error_code`,
`url`, `timings`) plus `closed`, and they are updated as the session
goes.

**A failed handshake gives a closed socket with the error.** `send`
returns `false` and `receive` returns `null`; nothing throws.

**`socket.receive(timeoutMs = 30000)`** returns the next message, a
string or an `ArrayBuffer`, or `null` when:

- **the timeout passes:** `error_code` becomes `"timeout"` and it counts
  in `ws_errors`, but the socket stays open;
- **the socket closes;**
- **the test ends.**

**`send`, `sendBinary` and `close`** are as in the callback style.
`close` waits until the socket is closed.

### Latency

**In this style, every send is timed by default** until a later
`receive` returns a message, matched in order. Opt out with
`{ reply: false }`.

Calling `receive` is the script saying it expects a reply, so timing by
default fits this style. The callback style keeps `{ reply: true }` as
an opt-in, because handlers also see messages the server pushes on its
own.

### Ownership and cleanup

**A blocking socket belongs to the iteration (or setup, or teardown)
that opened it,** and is kept in that VU's instance:

- the reader goroutine and the bounded channel are as in the callback
  style;
- the script pulls messages with `receive`, instead of an event loop
  pushing them to handlers.

**A socket the script leaves open is closed when the iteration
returns,** with a normal close and a once-per-run warning. This needs
an end-of-iteration hook: `protocol.Instance.EndIteration` (ADR-018
amendment).

**When the test ends,** sockets close at once and nothing is recorded,
as in the callback style. `Instance.Close` closes anything still open.

### Name

**`loadtool/websocket` is an alias for `loadtool/ws`,** through the new
optional `protocol.Aliased` interface. It is one module with one set of
metrics.

### Not covered

**Choosing a message by content.** `receive` returns the next message;
a reply that arrives out of order (or a pushed message) is still
returned next.
