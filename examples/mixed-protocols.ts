/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import ws from "loadtool/websocket";
import grpc from "loadtool/grpc";
import graphql from "loadtool/graphql";
import kafka from "loadtool/kafka";
import { check } from "loadtool";

// Five protocols in one iteration, as one user journey: log in over
// HTTP, then use the session's token with GraphQL, talk over WebSocket,
// call a gRPC service and publish an event to Kafka. Checks, thresholds,
// the console summary and the JSON and HTML reports cover all of them.
// The execution flow is described in docs/mixed-protocols.md.
//
// Start the demo API (HTTP and WebSocket on 8090, gRPC on 8091, an
// in-process Kafka on 9092), then run:
//   go run ./examples/server
//   loadtool run examples/mixed-protocols.ts --vus 10 --duration 10s
//
// Every check below compares with something only this VU sent, so state
// leaking between VUs would fail it.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const WS_URL = BASE_URL.replace(/^http/, "ws") + "/ws/echo";
const GRPC_ADDR = __ENV.GRPC_ADDR || "127.0.0.1:8091";
const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");

// Top-level code runs once in every VU: each VU gets its own gRPC client,
// GraphQL client and Kafka producer, none of which connects yet.
const grpcClient = new grpc.Client();
grpcClient.load(["proto"], "greeter.proto"); // relative to this script
const api = new graphql.Client(`${BASE_URL}/graphql`);
const events = new kafka.Producer({ brokers: BROKERS, topic: "events" });

// Per-VU state, kept across this VU's iterations.
let grpcConnected = false;

export const options = {
  thresholds: {
    checks: ["rate==1"],
    http_req_failed: ["rate==0"],
    ws_session_failed: ["rate==0"],
    grpc_req_failed: ["rate==0"],
    graphql_req_failed: ["rate==0"],
    kafka_produce_failed: ["rate==0"],
    ws_msg_latency: ["p(95)<500"],
    grpc_req_duration: ["p(95)<500"],
    kafka_produce_duration: ["p(95)<500"],
  },
};

// setup runs once, before the VUs; what it returns is every iteration's
// data. Its requests are not part of the load metrics.
export function setup() {
  const res = http.get(`${BASE_URL}/health`);
  if (res.status !== 200) throw new Error(`the API is not up: ${res.status}`);
  return { productIds: [1, 2, 3, 4, 5] };
}

export default function (data: { productIds: number[] }): void {
  const user = `vu${__VU}`;
  const tag = `${user}-${__ITER}`;

  // 1. HTTP: log in, then read the session back with its token.
  const login = http.post(`${BASE_URL}/api/login`, JSON.stringify({ username: user, password: "demo" }), {
    headers: { "Content-Type": "application/json" },
    responseType: "text",
  });
  const token = login.status === 200 ? (login.json() as { token: string }).token : "";
  const auth = { Authorization: `Bearer ${token}` };
  const me = http.get(`${BASE_URL}/api/me`, { headers: auth, responseType: "text" });
  check(me, { "http: logged in as this VU": (r) => r.status === 200 && (r.json() as { username: string }).username === user });

  // 2. GraphQL, over the same HTTP connections, with the same token.
  const id = data.productIds[__ITER % data.productIds.length];
  const product = api.query("query ($id: Int!) { product(id: $id) { id name } }", { variables: { id }, headers: auth });
  check(product, { "graphql: product found": (r) => r.ok && r.data.product.id === id });

  // 3. WebSocket: a request/reply exchange on a socket of this iteration.
  const socket = ws.connect(WS_URL);
  socket.send(tag);
  const echo = socket.receive(5000);
  socket.close();
  check(echo, { "websocket: echoed this VU's message": (m) => m === tag });

  // 4. gRPC: connect once per VU, then call on the kept connection.
  if (!grpcConnected) {
    const conn = grpcClient.connect(GRPC_ADDR, { plaintext: true });
    grpcConnected = conn.error === "";
  }
  const hello = grpcClient.invoke("greeter.Greeter/SayHello", { name: user }, { timeout: "2s" });
  check(hello, { "grpc: greeted this VU": (r) => r.status === 0 && r.message.message === `Hello, ${user}` });

  // 5. Kafka: publish an event, keyed by user, and wait for the broker.
  const sent = events.produce({ key: user, value: JSON.stringify({ user, iter: __ITER, product: id }) });
  check(sent, { "kafka: event acknowledged": (r) => r.ok && r.offset >= 0 });
}

// teardown runs once, after the VUs, with setup's data.
export function teardown(data: { productIds: number[] }) {
  console.log(`mixed-protocols: done (${data.productIds.length} products)`);
}
