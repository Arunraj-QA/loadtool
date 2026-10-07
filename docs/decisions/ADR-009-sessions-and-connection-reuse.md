# ADR-009: Sessions (cookies) and connection reuse

- Status: Accepted
- Date: 2026-10-06

## Context

Phase 1 roadmap items 7 and 8 are connection reuse and cookie/session
handling.

- **The HTTP client today.** All VUs share one `http.Client` and one
  transport (ADR-003).
  - Keep-alive is on, and the pool allows one connection per VU per host.
  - There is no cookie jar: a `Set-Cookie` is ignored, so a script cannot
    log in and keep the session.
- **The constraint.** Per-VU memory is the Phase 0 exit criterion.
  - A per-VU `http.Transport` would duplicate pools, maps and locks in
    every VU.
  - So would a cookie jar created eagerly for every VU.

The API follows k6's shape (ADR-005).

## Decision

1. **One session per VU.** Each VU gets its own `http.Client`, a small
   struct that shares the run's transport (so connection pooling is
   unchanged) and holds the VU's cookie jar.
2. **The jar is lazy.**
   - `httpclient.Jar` wraps `net/http/cookiejar` and creates it only when
     a response first sets a cookie.
   - A test whose responses set no cookies allocates no jar, and the
     per-request jar lookup returns at once.
   - A VU's jar is used only from that VU's goroutine.
3. **Cookies are cleared at the start of every iteration**, as in k6, so
   each iteration is a fresh visitor.
   - `options.noCookiesReset: true` keeps one session per VU for the whole
     test.
   - Clearing drops the jar (sets it to nil); it allocates nothing.
4. **Script API:**
   - Responses' cookies are stored in the jar and sent on later requests
     to the same site, following the cookies' domain, path, expiry and
     secure rules (`net/http/cookiejar`, no public-suffix list).
   - `params.cookies: { name: "value" }` adds cookies to one request, on
     top of the jar's.
   - `res.cookies` is `{ name: [{ name, value, domain, path, expires,
     max_age, http_only, secure }] }`, the cookies that response set (k6's
     shape). It is built only when read.
   - k6's `http.cookieJar()` (reading and editing the jar from the script)
     is not part of Phase 1.
5. **setup and teardown have their own jar.** Cookies set there do not
   reach the VUs; data passes through setup's return value (ADR-008).
6. **Connection reuse:**
   - Keep-alive reuse stays the default.
   - `options.noConnectionReuse: true` disables keep-alive, so every request
     opens a new connection. This tests handshake cost or servers behind
     connection-tracking balancers.
   - k6's `noVUConnectionReuse` (a new connection per iteration) would need
     per-VU transports. It is not supported, and the unknown-option warning
     says so.

## Consequences

- **Login flows work** without script changes: a `POST /login` that sets a
  session cookie authenticates the rest of the iteration.
- **Per-VU memory** grows by one `http.Client` and one `Jar` wrapper:
  - The cookie jar itself exists only in VUs whose responses set cookies.
  - Measured with `BenchmarkVURetainedMemory` (no imports): 4,661 B →
    4,720 B per VU, so +59 B.
  - Iterations allocate exactly as before: `BenchmarkIterateEmpty` 6
    allocations and `BenchmarkIterateHTTPGet` 81, before and after.
- **Per-request cost.** A response that sets cookies costs the jar's
  parsing and storage, but only then; no other request pays anything.
- **Redirects** are still not followed (ADR-003), so cookies set by a
  redirect response are stored and sent only on the script's next request.

## Hardening (2026-10-07)

**Tests added:**

- *Cookie rules*, through the real client and jar (`TestCookieSemantics`,
  `TestCookieReplaceAndDelete`):
  - host-only and domain cookies;
  - path matching;
  - `Secure` over http;
  - `Max-Age=0` and past `Expires`;
  - replacement, and deletion on logout.
- *End to end through the runner* (`TestRunLoginFlow`): log in, use the
  session, log out, refused afterwards; with checks and thresholds.
- *Concurrent isolation*
  (`TestRunSessionsAreIsolatedAcrossConcurrentVUs`): 50 VUs log in at once
  and every request is checked, on the server side, to carry its own VU's
  session. It runs with the jar reset each iteration and with
  `noCookiesReset`, under the race detector in CI. It also checks that
  cookies do not prevent connection reuse (at most one connection per
  VU).

**Mutation checks:**

- Sharing one jar between all VUs makes the isolation test fail with
  over 10,000 iterations carrying another VU's session.
- Removing the per-iteration reset makes about 27,000 iterations start
  logged in.

**Shared-state audit.** Package-level variables in `internal/script` and
`internal/httpclient` are read-only (errors, property tables, module
sources). Session state lives only in each VU's `Jar` and client copy.

