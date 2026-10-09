# Examples

Twenty-one runnable scripts. They all run against the demo API in
[`server/`](server/), a small shop with products, orders, a login, a
WebSocket echo, a gRPC greeter, a GraphQL endpoint and an in-process
Kafka broker, so
you can try every feature without a server of your own. CI runs every
example on every change (`scripts/smoke-examples.sh`), so they keep
working.

```bash
go run ./examples/server               # in another terminal; 127.0.0.1:8090
loadtool run examples/checks.ts --vus 5 --duration 10s
```

## The scripts

| Example | Shows |
|---|---|
| [`basic-http.ts`](basic-http.ts) | A GET API test with constant VUs (`--vus`, `--duration`) |
| [`basic-http.js`](basic-http.js) | The same in plain JavaScript |
| [`checks.ts`](checks.ts) | `check` on status, headers, JSON bodies and timings; `sleep` |
| [`thresholds.ts`](thresholds.ts) | Pass/fail criteria and exit code 99 |
| [`post-json.ts`](post-json.ts) | POST a JSON body and check the JSON response |
| [`auth-token.ts`](auth-token.ts) | Log in once in `setup()`, send a bearer token from every VU; `teardown()` |
| [`sessions.ts`](sessions.ts) | Cookie login: each VU's own session cookie, sent automatically; `res.cookies`; logout |
| [`data-driven.ts`](data-driven.ts) | Test data from a JSON file and a shared module ([`lib/api.ts`](lib/api.ts), [`data/products.json`](data/products.json)) |
| [`scenarios.ts`](scenarios.ts) | Three workloads at once: `constant-vus`, `ramping-vus` and `constant-arrival-rate` (20 s) |
| [`http2.ts`](http2.ts) | Requiring HTTP/2 with `httpVersion: "2"`, checking `res.proto` |
| [`websocket.ts`](websocket.ts) | HTTP and WebSocket in one iteration: log in, then send messages and time each reply; WebSocket thresholds |
| [`websocket-request-reply.ts`](websocket-request-reply.ts) | WebSocket in the blocking style: `connect`, `send`, `receive`, `close` |
| [`grpc-unary.ts`](grpc-unary.ts) | gRPC unary calls from a `.proto` file ([`proto/greeter.proto`](proto/greeter.proto)): metadata, deadline, checks, thresholds |
| [`grpc-streaming.ts`](grpc-streaming.ts) | gRPC by server reflection: server, client and bidirectional streams |
| [`graphql-query.ts`](graphql-query.ts) | GraphQL queries with variables, a client with default headers |
| [`graphql-mutation.ts`](graphql-mutation.ts) | A GraphQL mutation with variables, after an HTTP login (bearer token header) |
| [`graphql-errors.ts`](graphql-errors.ts) | GraphQL errors with HTTP 200: `http_ok` true, `ok` false, partial data |
| [`kafka-producer.ts`](kafka-producer.ts) | A Kafka producer per VU: keys, headers, acknowledgement checks and thresholds |
| [`kafka-consumer.ts`](kafka-consumer.ts) | Producing and consuming at once (two scenarios), a consumer group, end-to-end latency |
| [`kafka-throughput.ts`](kafka-throughput.ts) | Batches of 100 messages per call; a threshold on messages per second |
| [`kafka-errors.ts`](kafka-errors.ts) | Kafka errors in results: an unknown topic, an invalid message, an unreachable broker |

**By topic:**

| Topic | Example |
|---|---|
| GET API | `basic-http.ts`, `checks.ts` |
| POST JSON | `post-json.ts` |
| Authentication | `auth-token.ts` (bearer token), `sessions.ts` (cookie) |
| Cookies and sessions | `sessions.ts` |
| Checks | `checks.ts`, every other example |
| Thresholds | `thresholds.ts` |
| Constant VUs | `basic-http.ts`, `scenarios.ts` |
| Ramping VUs | `scenarios.ts` |
| Constant arrival rate | `scenarios.ts` |
| HTTP/2 | `http2.ts` |
| WebSocket | `websocket.ts` (callbacks), `websocket-request-reply.ts` (blocking) |
| gRPC | `grpc-unary.ts` (`.proto`), `grpc-streaming.ts` (reflection, streams) |
| GraphQL | `graphql-query.ts`, `graphql-mutation.ts`, `graphql-errors.ts` |
| Kafka | `kafka-producer.ts`, `kafka-consumer.ts`, `kafka-throughput.ts`, `kafka-errors.ts` |
| Setup and teardown | `auth-token.ts` |

## Against your own server

Every example reads `BASE_URL` from the environment (the gRPC ones
`GRPC_ADDR`, and the Kafka ones `KAFKA_BROKERS`, comma-separated):

```bash
loadtool run -e BASE_URL=https://staging.example.test examples/basic-http.ts
loadtool run -e KAFKA_BROKERS=broker1:9092,broker2:9092 examples/kafka-producer.ts
```

The paths (`/api/products`, `/api/login`, …) are the demo API's; change
them to yours.

**Credentials.** `auth-token.ts` and `sessions.ts` read `API_USER` and
`API_PASSWORD`, so they never need to be written in a script.

## The demo API

```bash
go run ./examples/server -addr 127.0.0.1:8090 -delay 5ms
```

| Endpoint | Answer |
|---|---|
| `GET /health` | `{"status":"ok"}` |
| `GET /api/products` | `{"products": [{"id", "name", "priceCents"}, …]}` (5 products) |
| `GET /api/products/{id}` | One product; 404 if unknown |
| `POST /api/orders` | `{"productId": 1, "quantity": 2}` gives 201 with `{"id", "productId", "quantity", "totalCents"}`; 400 if invalid |
| `POST /api/login` | `{"username": "any", "password": "demo"}` gives `{"username", "token"}` and a `session` cookie; 401 for another password |
| `GET /api/me` | `{"username"}` from the `session` cookie or `Authorization: Bearer <token>`; 401 without one |
| `POST /api/logout` | 204; deletes the `session` cookie |
| `GET /ws/echo` | WebSocket: echoes every message (text or binary) after `-delay` |
| `POST /graphql` | GraphQL (after `-delay`): `products`, `product(id)` (an error for an unknown id), `fail(message)`, mutation `placeOrder(productId, quantity)` |
| gRPC on `-grpc-addr` (`127.0.0.1:8091`) | `greeter.Greeter` from `proto/greeter.proto`, with server reflection: `SayHello` (after `-delay`), `LotsOfReplies` (each reply after `-delay`), `LotsOfGreetings`, `Chat`, `Fail`; `fail_code` on a streaming request ends the stream with that status |
| Kafka on `127.0.0.1:` `-kafka-port` (`9092`; `0` turns it off) | An in-process broker (franz-go's kfake, not a real Kafka) with the topics `orders` and `events`, 3 partitions each, in memory. The Kafka examples read `KAFKA_BROKERS` to use a real broker instead (`testenv/kafka/up.sh` starts one in Docker) |

**How it behaves:**

- It speaks HTTP/1.1 and HTTP/2 without TLS (h2c) on the same port.
- Every `/api/` request waits `-delay` (default 5 ms) first.
- Sessions are signed, not stored, so its memory stays flat under load.
  They are invalidated when it restarts.
- It is for trying LoadTool out. Benchmarks use
  [`benchmarks/server`](../benchmarks/server/) instead.

## Example report

[`reports/example-report.html`](reports/example-report.html) shows what
`--report-html` writes: a failed run with an error burst. It uses sample
data, not a measurement; open the file in a browser.

## Editor support

Each example starts with a reference to
[`types/loadtool.d.ts`](../types/loadtool.d.ts), which gives editors
completion and type hints. LoadTool itself does not type-check.
