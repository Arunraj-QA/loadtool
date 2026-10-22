# HTTP (`loadtool/http`)

[Script API](../script-api.md) · Protocols: [HTTP](http.md) · [WebSocket](websocket.md) · [gRPC](grpc.md) · [GraphQL](graphql.md) · [Kafka](kafka.md)

```typescript
import http from "loadtool/http";            // the module object
import { get, post } from "loadtool/http";   // or single functions
```

| Function | Notes |
|---|---|
| `get(url, params?)` | |
| `post(url, body?, params?)` | `put`, `patch` and `del` take the same arguments |
| `request(method, url, body?, params?)` | Any method, e.g. `request("OPTIONS", url)` |

**`body`** is a string.

- To send JSON, use `JSON.stringify(...)` with a
  `Content-Type: application/json` header.
- An object body is a `TypeError`, not silently form-encoded.

```typescript
http.post(`${BASE}/api/orders`, JSON.stringify({ productId: 1, quantity: 2 }), {
  headers: { "Content-Type": "application/json" },
});
```

([`examples/post-json.ts`](../../examples/post-json.ts) checks the response.)

**`params`** is `{ headers: { name: value }, cookies: { name: value },
responseType: "text" | "none" }`. Other keys (such as `timeout` or
`tags`) produce one warning per run and are ignored.

**Requests never throw on network errors.**

- A refused connection or a timeout (30 s per request) gives `status: 0`
  and an `error` message, and counts as a failed request.
- Redirects are not followed: a 3xx response is returned as it is.
- LoadTool sends only the headers you set; it does not add
  `Accept-Encoding: gzip`.

## Responses

| Field | Value |
|---|---|
| `status` | HTTP status, or `0` if no response was received |
| `proto` | `"HTTP/1.1"` or `"HTTP/2.0"`; `""` without a response |
| `error` | `""`, or the network error |
| `headers` | `{ "Content-Type": "..." }`, canonical names; repeated headers joined with `", "` |
| `body` | The body as a string; `null` if it was discarded (the default) or nothing arrived |
| `json()` | The body parsed as JSON; throws `SyntaxError` on invalid JSON, and `TypeError` if the body was discarded |
| `timings.duration` | Milliseconds from sending the request to reading the whole body |
| `url` | The request URL |
| `cookies` | Cookies this response set: `{ name: [{ name, value, domain, path, expires, max_age, http_only, secure }] }` |

**Response bodies are discarded by default.** Each body is read in full
(so timings include it), then thrown away. That keeps memory flat
whatever the response size: keeping 1 MB responses at 1,000 VUs needs
about 2 GB (ADR-013). To read bodies, ask for them:

| To keep | Set |
|---|---|
| Every body | `options.discardResponseBodies: false` |
| One request's body | `responseType: "text"` in that request's params |
| Not this one, when every body is kept | `responseType: "none"` |

```typescript
const res = http.get(`${BASE}/api/products/3`, { responseType: "text" });
check(res, { "right one": (r) => r.json().id === 3 });
```

**Reading a discarded body:**

- `res.body` is `null`, and LoadTool warns once per run, saying how to
  keep it.
- `res.json()` throws a `TypeError` with the same advice, so a check that
  uses it fails with that message.
- `status`, `headers`, `cookies` and timings are always available.
- Bodies in `setup` and `teardown` are always kept.

A request succeeds when it gets a 2xx or 3xx response.

## Cookies and sessions

**Each VU has its own cookie jar**
([`examples/sessions.ts`](../../examples/sessions.ts)):

- Cookies that responses set are stored and sent on later requests to the
  same site, following their domain, path, expiry and `Secure` rules.
- Every iteration starts with an empty jar, like a new visitor.
  `options.noCookiesReset: true` keeps one session per VU for the whole
  test.
- VUs never see each other's cookies.
- `setup` and `teardown` share their own jar, so teardown can log out of
  setup's session.

```
VU 1 ─ session 1 ─ cookie jar A ─┐
VU 2 ─ session 2 ─ cookie jar B ─┼─ one shared connection pool
VU 3 ─ session 3 ─ cookie jar C ─┘
```

**Cookie rules** (RFC 6265, as implemented by Go's `net/http/cookiejar`):

| Cookie | Sent to |
|---|---|
| No `Domain` (host-only) | The host that set it only; not its subdomains or other hosts |
| `Domain=app.test` | `app.test` and its subdomains |
| `Path=/admin` | `/admin` and paths below it |
| `Secure` | `https://` requests only |

**Replacing and deleting:**

- A cookie with the same name, domain and path replaces the earlier one.
- `Max-Age=0`, a negative `Max-Age` or a past `Expires` deletes it: the
  usual logout.
- A cookie without `Max-Age` or `Expires` lasts until the jar is reset.
- No public-suffix list is used; it matters only for cookies set on
  domains like `co.uk`, which a test target should not do.

**Script access.** `params.cookies` adds cookies to one request, and
`res.cookies` shows what a response set. Reading and editing the jar
directly is not supported yet.

**Headers are per request.** Set them with `params.headers`; there are no
per-session default headers. For a header on every request, keep a params
object in a constant and pass it to each call.

## HTTP versions and connections

`options.httpVersion` selects the protocol
([`examples/http2.ts`](../../examples/http2.ts)):

| Value | `http://` | `https://` |
|---|---|---|
| `"auto"` (default) | HTTP/1.1 | HTTP/2 if the server offers it, else HTTP/1.1 |
| `"1.1"` | HTTP/1.1 | HTTP/1.1 |
| `"2"` | HTTP/2 without TLS (h2c) | HTTP/2 only |

- **No fallback with `"2"`.** A server without HTTP/2 fails the request
  rather than being measured over HTTP/1.1.
- **Seeing the protocol.**
  - `res.proto` shows the protocol each response used.
  - The summary has a `Protocols:` line (for example `HTTP/2 9,000,
    HTTP/1.1 12`) whenever HTTP/2 was used, so you can tell whether
    `"auto"` negotiated it.
- **Extra connections.** HTTP/2 sends many VUs' requests over one
  connection. When every connection is at the server's stream limit, a
  new connection is dialled for each waiting request, so pushing a server
  far past its limit causes a burst of connections (as HTTP/1.1 does at
  start-up).
- **Low stream limits.** A server that allows fewer than 100 concurrent
  streams can refuse the first requests on a new connection; they count
  as failed requests.

**Connections:**

- They are kept alive and reused. All VUs share one pool with up to one
  connection per VU per host; HTTP/2 carries many requests per
  connection.
- `options.noConnectionReuse: true` opens a new connection for every
  request.
- Certificates are always verified.

## Examples

Runnable against the demo API ([how](../../examples/README.md)):

| Example | Shows |
|---|---|
| [`basic-http.ts`](../../examples/basic-http.ts) | GET requests with checks |
| [`post-json.ts`](../../examples/post-json.ts) | POST JSON |
| [`auth-token.ts`](../../examples/auth-token.ts) | A bearer token from setup |
| [`sessions.ts`](../../examples/sessions.ts) | Cookie sessions |
| [`http2.ts`](../../examples/http2.ts) | HTTP/2 |
| [`scenarios.ts`](../../examples/scenarios.ts) | Scenarios: constant VUs, ramping, arrival rate |
