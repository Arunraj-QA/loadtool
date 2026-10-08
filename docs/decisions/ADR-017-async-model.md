# ADR-017: Asynchronous protocols without a global event loop

- Status: Proposed (2026-10-08, Phase 2)
- Date: 2026-10-08

## Context

A VU is one goroutine running one goja runtime. Script calls are
synchronous: `http.get` blocks the VU until the response arrives. There
is no event loop, no Promises and no timers outside `sleep`.

WebSocket is event-driven: messages arrive at any time, and scripts
react to them. A Kafka consumer polls. gRPC unary calls and GraphQL fit
the synchronous model as they are.

**A goja runtime is not safe for concurrent use.** A callback must run on
the VU's own goroutine, never on a network goroutine.

## Decision

### 1. Request/response protocols block, like HTTP

These calls run on the VU goroutine and return a result:

- gRPC unary calls (`client.invoke`);
- GraphQL calls;
- Kafka `produce`;
- Kafka `consume` with a timeout.

### 2. WebSocket: a blocking session with a session-scoped loop

This follows k6's `k6/ws` shape:

```ts
ws.connect(url, params, (socket) => {
  socket.on("open", () => socket.send("hi"));
  socket.on("message", (msg) => { /* ... */ });
  socket.setTimeout(() => socket.close(), 2000);
});
// connect returns when the socket is closed
```

**How the loop works:**

- **One reader goroutine per session** reads frames and sends them on a
  bounded channel.
- **The VU goroutine runs the loop inside `connect`.** It dispatches
  `open`, `message`, `ping`/`pong`, `close` and `error` callbacks, plus
  timers (`setTimeout`, `setInterval`), and returns when the socket is
  closed.
- **The test ending** (`vu.Context()` done) closes the socket. The
  reader goroutine exits, the loop returns, and nothing more is
  recorded.
- **A callback that throws** closes the session. The exception
  propagates from `connect`, which ends the iteration as a script error,
  as any throw does today.
- **Reader goroutines never outlive the session.** Tests check goroutine
  counts.

### 3. No global event loop or Promises in Phase 2

Each session's loop exists only while `connect` runs.

## Consequences

- **The VU model is unchanged.** Iterations stay sequential, and the
  engine and scenario timing are untouched.
- **One VU holds one WebSocket session at a time.** Many concurrent
  sessions need many VUs, as many connections do in HTTP.
- **Familiar to k6 users.** Scripts written for `k6/ws` port with only
  the import path changed, apart from differences listed in the
  WebSocket ADR.
- **Room to grow.** A Promise-based API could later be added on a
  general event loop, without breaking these blocking APIs.

## Alternatives considered

- **A general per-VU event loop with Promises,** like k6's newer
  experimental WebSocket API: rejected for Phase 2. It changes how
  every iteration ends (it must wait for pending work), and it affects
  timing, cancellation and memory for all protocols.
- **Callbacks on network goroutines:** rejected. goja is not safe for
  concurrent use.
