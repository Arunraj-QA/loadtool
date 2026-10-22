# ADR-021: GraphQL module

- Status: Accepted (2026-10-09, Phase 2)
- Date: 2026-10-09
- Builds on: ADR-014 to ADR-018 (protocol modules), ADR-009 (sessions),
  ADR-010 (HTTP/2), ADR-016 (errors)

## Context

The roadmap's GraphQL goal, as the user set it on 2026-10-09:

- **Operations:** queries, mutations, variables and request headers.
- **Results:** response access, GraphQL error detection, latency, checks
  and thresholds, and the JSON and HTML reports.

**GraphQL is an HTTP-based protocol module, not a new transport.** The
user's rule: do not duplicate the HTTP client; reuse the HTTP/1.1 and
HTTP/2 transport and the connection and session handling.

**HTTP success and GraphQL success must be told apart.** An HTTP 200
response whose body carries GraphQL `errors` is a failed operation.

**Phase 2 scope decision 1:** GraphQL calls made with `loadtool/graphql`
are counted only under `graphql_*` metrics, never under `http_*`.

## Decision

### Transport: the existing HTTP stack, unchanged

**`httpclient.Do` is split in two:**

- **`httpclient.Send`** builds, sends and times one request and reads
  its body: everything except recording.
- **`Do`** is `Send` followed by the HTTP metrics, so HTTP's behaviour,
  timing and hot path are unchanged.

**GraphQL calls `Send` with the VU's HTTP session (`VU.HTTPClient()`).**
That is the run's shared transport and connection pool, with this VU's
cookie jar. So GraphQL requests:

- use HTTP/1.1 or HTTP/2 as `options.httpVersion` says (ADR-010);
- reuse keep-alive connections, with `noConnectionReuse` respected
  (ADR-009);
- send and store the VU's cookies, so a login over `loadtool/http`
  carries over (ADR-009);
- verify certificates and keep the 30 s request timeout.

**No GraphQL client library is used.** A GraphQL-over-HTTP request is a
JSON POST, `{ "query", "variables", "operationName" }`, sent with:

- `Content-Type: application/json`;
- `Accept: application/graphql-response+json, application/json`.

### API (`loadtool/graphql`)

```ts
import graphql from "loadtool/graphql";

const res = graphql.query(url, `query ($id: Int!) { product(id: $id) { name } }`, {
  variables: { id: 3 }, headers: { Authorization: `Bearer ${token}` },
});
const created = graphql.mutation(url, `mutation ($p: Int!) { placeOrder(productId: $p, quantity: 1) { id } }`, {
  variables: { p: 1 },
});

const api = new graphql.Client(url, { headers: { Authorization: `Bearer ${token}` } });
api.query(`{ products { id } }`);
```

| Call | Behaviour |
|---|---|
| `query(url, document, params?)`, `mutation(url, document, params?)` | One operation |
| `params` | `{ variables, headers, operationName, timeout }`; `timeout` in ms or as `"2s"` |
| `new graphql.Client(url, { headers })` | Keeps the endpoint and default headers. `client.query(document, params?)` and `client.mutation(...)` merge their headers over the defaults. |

**`query` and `mutation` send the same request.** GraphQL tells them
apart by the document. Two names make scripts read as what they do, and
the result carries the operation kind for checks.

### The result: transport and application, apart

| Field | Value |
|---|---|
| `status`, `proto`, `headers` | The HTTP response, as in `loadtool/http` |
| `http_ok` | **Transport success:** a response with HTTP status 2xx |
| `data` | The response's `data` (an object, or `null`) |
| `errors` | The response's `errors`, always an array (`[]` when none) |
| `ok` | **GraphQL success:** `http_ok`, a JSON body, and no `errors` |
| `error`, `error_code` | Why it is not `ok`, in the shape of ADR-016 (below) |
| `body` | The raw response body |
| `timings.duration` | Milliseconds, timed by `httpclient.Send` |

**Every combination is represented:**

| Response | `http_ok` | `ok` | `error_code` | Counted in |
|---|---|---|---|---|
| 200, `data`, no `errors` | true | true | `""` | — |
| **200 with `errors`** (partial `data` possible) | **true** | **false** | `server` | `graphql_req_failed`, `graphql_errors` |
| 4xx or 5xx (a GraphQL-over-HTTP error, or a gateway error) | false | false | `server` | `graphql_req_failed`, plus `graphql_errors` if the body had `errors` |
| 2xx with a body that is not JSON | true | false | `protocol` | `graphql_req_failed` |
| No response (refused, DNS, TLS, timeout) | false | false | `dial`, `dns`, `tls`, `timeout`, `closed` | `graphql_req_failed` |
| An invalid URL, or variables that are not JSON-serializable | false | false | `invalid` (never sent) | `graphql_req_failed` |

**`error`** is the transport error, the HTTP status, or the first
GraphQL error's `message` (with the count when there are several).

**Failures are returned, never thrown** (ADR-016). Misuse throws a
`TypeError`:

- a missing URL or document;
- a call in top-level code.

### Metrics (ADR-015)

| Family | Kind | Meaning |
|---|---|---|
| `graphql_req_duration` | Trend | Operation latency, failed operations included |
| `graphql_reqs` | Counter | Operations |
| `graphql_req_failed` | Rate | Operations that were not `ok` |
| `graphql_errors` | Counter | Operations whose response carried GraphQL `errors` (an application error, distinct from transport failures) |

**GraphQL requests are not counted in `http_*`** (scope decision 1), so
`http_req_failed` and `graphql_req_failed` never disagree about the same
request.

### Ownership and lifecycle (ADR-018)

- **No state of its own.** The module uses the VU's HTTP session, so
  connection ownership is HTTP's: a run-wide pool and a per-VU cookie
  jar, reset each iteration unless `noCookiesReset`.
- **The run holds only the family IDs.**
- **Each operation is one blocking call** on the VU's goroutine, bounded
  by the VU's context and the timeout. When the test ends, it is not
  recorded.

### Test service

**`internal/protocols/graphql/graphqltest`** is a small shop schema,
served over HTTP by **`github.com/graphql-go/graphql`** (MIT). It is a
real GraphQL executor, so validation, variables, partial data and
errors follow the GraphQL specification. It offers:

- `products`;
- `product(id)`, which gives an error for an unknown ID;
- `placeOrder(productId, quantity)`, a mutation with validation;
- `fail(message)`, which always errors.

**The demo API serves it at `POST /graphql`.** The executor is used only
by the test service and the demo API, never by the module.

## Consequences

- **HTTP's code paths stay single.** A change to HTTP timing or
  transport applies to GraphQL too.
- **Scripts that already send GraphQL with `http.post`** keep working
  unchanged and are counted as HTTP.
- **Not supported yet:**
  - subscriptions (WebSocket- or SSE-based);
  - GET requests;
  - persisted queries;
  - batching;
  - multipart uploads.
