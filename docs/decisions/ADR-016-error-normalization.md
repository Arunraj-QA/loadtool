# ADR-016: Normalized errors across protocols

- Status: Accepted (2026-10-08, Phase 2)
- Date: 2026-10-08

## Context

HTTP calls never throw on network failures:

- status `0` and an `error` message mean no response arrived;
- a status of 400 or more counts as a failed request.

Script misuse, such as an object as a body, throws a `TypeError`. Calls
cut short by the end of the test are not recorded.

The Phase 2 protocols fail in different ways:

- **gRPC** returns a status code.
- **GraphQL** can fail inside an HTTP 200 (`errors[]`).
- **WebSocket** can fail at the handshake or close abnormally.
- **Kafka** reports a broker error per record.

Scripts, checks and the troubleshooting guide need one way to tell what
went wrong.

## Decision

### 1. Every result object has two common fields

| Field | Value |
|---|---|
| `error` | `""`, or a human-readable message, as `res.error` is today |
| `error_code` | `""`, or one of the categories below |

| `error_code` | Meaning |
|---|---|
| `dns` | The host name did not resolve |
| `dial` | The connection was refused or unreachable |
| `tls` | A TLS or certificate failure |
| `timeout` | No complete answer in time (not the end of the test) |
| `protocol` | A malformed or unexpected protocol exchange, such as a failed WebSocket handshake or bad frames |
| `server` | The other side answered with an application error: HTTP status ≥ 400, gRPC status ≠ OK, GraphQL `errors`, a Kafka broker error, an abnormal WebSocket close |
| `closed` | The connection closed while the call was in progress |
| `invalid` | The request could not be built, such as an invalid URL (the call was never sent) |

### 2. Each protocol keeps its native detail

- HTTP: `status`;
- gRPC: `status` (the numeric code) and `status_text`;
- GraphQL: `errors`;
- Kafka: the broker error name;
- WebSocket: the close code.

### 3. One classifier for transport errors

`protocol.Classify(err)` maps Go transport errors to a category:

- `*net.DNSError` → `dns`;
- `*net.OpError` while dialling → `dial`;
- TLS and x509 errors → `tls`;
- `os.ErrDeadlineExceeded` and `context.DeadlineExceeded` not caused by
  the end of the test → `timeout`.

Each module maps its own application errors to `server`.

### 4. HTTP responses get `error_code` too

This is additive, and costs nothing for successful responses.

### 5. Failures are returned, not thrown

| Situation | Handling |
|---|---|
| A network or protocol failure | Returned in the result and recorded as failed in the protocol's metrics |
| Script misuse (wrong arguments, a call in top-level code) | Throws a `TypeError`, which is counted as a script error |
| A call cut short by the end of the test | Not recorded, never shown |

### 6. What counts as failed for metrics

Each protocol ADR defines it. It is always a non-empty `error_code`, so
`<protocol>_req_failed` and `error_code` agree.

## Consequences

- **Checks can branch on categories** across protocols, such as
  `r.error_code === "timeout"`, and the troubleshooting guide documents
  one table.
- **One extra string field per failed HTTP response.** Successful
  responses are unchanged.
- **Classification needs care.** A cancellation must be told apart from
  a timeout using the context's cause, as `httpclient.Do` already does by
  checking `ctx.Err()`.

## Alternatives considered

- **Throwing exceptions on failures:** rejected. One failed request
  would end the iteration, and it would be inconsistent with HTTP.
- **Native codes only:** rejected. Scripts and docs would need four
  vocabularies.
