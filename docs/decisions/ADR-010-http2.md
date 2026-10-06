# ADR-010: HTTP/2

- Status: Accepted
- Date: 2026-10-06
- Amends: ADR-003 (HTTP/2 negotiation was disabled, so HTTPS used HTTP/1.1)

## Context

Phase 1 roadmap item 6 is HTTP/2.

- **Today.** Phase 0 forces HTTP/1.1 even over TLS (ADR-003), so its
  numbers are comparable with JMeter's.
- **k6** negotiates HTTP/2 over TLS through ALPN by default, and has no
  cleartext HTTP/2 (h2c).
- **Go 1.24+** supports HTTP/1.1, HTTP/2 over TLS and h2c in `net/http`
  through `http.Protocols`, with no extra dependency.

## Decision

1. **`options.httpVersion` selects the protocols:**

   | Value | `http://` URLs | `https://` URLs |
   |---|---|---|
   | `"auto"` (default) | HTTP/1.1 | HTTP/2 if the server offers it (ALPN), else HTTP/1.1 |
   | `"1.1"` | HTTP/1.1 | HTTP/1.1 (Phase 0 behaviour) |
   | `"2"` | h2c (HTTP/2 with prior knowledge) | HTTP/2 only; a server without HTTP/2 fails the request |

   - The default follows k6 (ADR-005). It only changes HTTPS targets:
     plain-HTTP targets, including the benchmark server, keep HTTP/1.1.
   - `"2"` does not fall back silently. A test that asks for HTTP/2 must
     not measure HTTP/1.1 without saying so.
2. **One shared transport** stays, configured with `Transport.Protocols`.
   - HTTP/2 multiplexes many VUs' requests over few connections. Go opens
     another connection when a server's concurrent-stream limit is
     reached.
   - `MaxConnsPerHost` (VUs) still caps the connections.
   - Per-VU cookie jars (ADR-009) work unchanged: cookies are per request,
     not per connection.
3. **`res.proto`** reports the protocol each response used, `"HTTP/1.1"` or
   `"HTTP/2.0"`, as in k6. A script can check it, for example
   `check(res, { h2: (r) => r.proto === "HTTP/2.0" })`.
4. **Compression stays off and redirects are not followed**, as in Phase 0,
   whatever the protocol.
5. **`noConnectionReuse`** applies to HTTP/2 too: each request opens a new
   connection.

## Consequences

- **HTTPS results change.** A test of an HTTPS server that supports
  HTTP/2 measures HTTP/2 from Phase 1 on: fewer connections and less
  handshake work.
  - `httpVersion: "1.1"` restores the Phase 0 behaviour, and is what a
    comparison with JMeter (HTTP/1.1 only) should use.
- **Untrusted certificates.** Certificate checks are unchanged. Servers
  with untrusted certificates still fail (`insecureSkipTLSVerify` is not
  part of Phase 1).
- **Benchmarks** (`http://`) are unaffected.
