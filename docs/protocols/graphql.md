# GraphQL (`loadtool/graphql`)

[Script API](../script-api.md) · Protocols: [HTTP](http.md) · [WebSocket](websocket.md) · [gRPC](grpc.md) · [GraphQL](graphql.md) · [Kafka](kafka.md)

GraphQL over LoadTool's HTTP transport: the same connections, HTTP/1.1
or HTTP/2 (`httpVersion`), and the VU's cookies as `loadtool/http`
([`examples/graphql-query.ts`](../../examples/graphql-query.ts),
[`graphql-mutation.ts`](../../examples/graphql-mutation.ts),
[`graphql-errors.ts`](../../examples/graphql-errors.ts)):

```typescript
import graphql from "loadtool/graphql";
import { check } from "loadtool";

const URL = "http://127.0.0.1:8090/graphql";

export default function () {
  const res = graphql.query(URL, "query ($id: Int!) { product(id: $id) { name } }", {
    variables: { id: 3 },
    headers: { Authorization: "Bearer token" },
  });
  check(res, {
    "HTTP ok": (r) => r.http_ok,
    "GraphQL ok": (r) => r.ok,
    "name": (r) => r.data.product.name === "Ceramic cup",
  });
}
```

| Call | |
|---|---|
| `query(url, document, params?)` | Run a query |
| `mutation(url, document, params?)` | Run a mutation |
| `new graphql.Client(url, { headers })` | An endpoint with default headers; `client.query(document, params?)`, `client.mutation(...)` |
| `params` | `{ variables, headers, operationName, timeout }` |

Each operation is a JSON POST (`{ query, variables, operationName }`).

**HTTP success and GraphQL success are separate:**

| Field | Value |
|---|---|
| `http_ok` | The transport succeeded: an HTTP 2xx response |
| `ok` | The operation succeeded: `http_ok`, a JSON response, and no GraphQL `errors` |
| `data` | The response's `data`, or `null` |
| `errors` | The response's `errors`; `[]` when none |
| `error`, `error_code` | Why it is not `ok` (below) |
| `status`, `proto`, `headers`, `body`, `timings.duration` | As for HTTP responses |

**How each kind of failure shows up:**

| What happened | `http_ok` | `ok` | `error_code` |
|---|---|---|---|
| Success | true | true | `""` |
| **HTTP 200 with GraphQL `errors`** (partial `data` possible) | true | false | `server` |
| HTTP 4xx or 5xx | false | false | `server` |
| A 2xx body that is not GraphQL JSON | true | false | `protocol` |
| No response | false | false | `dial`, `dns`, `tls`, `timeout`, `closed` |
| Invalid URL | false | false | `invalid` |

**Metrics:** GraphQL operations are counted under `graphql_*` only, not
under `http_*`:

| Metric | Kind | Meaning |
|---|---|---|
| `graphql_req_duration` | trend | Operation latency |
| `graphql_reqs` | counter | Operations |
| `graphql_req_failed` | rate | Operations that were not `ok` |
| `graphql_errors` | counter | Operations whose response carried GraphQL errors |

**Not supported yet:**

- subscriptions;
- GET requests;
- persisted queries;
- batching;
- file uploads.

## Examples

Runnable against the demo API ([how](../../examples/README.md)):

| Example | Shows |
|---|---|
| [`graphql-query.ts`](../../examples/graphql-query.ts) | Queries with variables |
| [`graphql-mutation.ts`](../../examples/graphql-mutation.ts) | A mutation after an HTTP login |
| [`graphql-errors.ts`](../../examples/graphql-errors.ts) | GraphQL errors with HTTP 200 |
