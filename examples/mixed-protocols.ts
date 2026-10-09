/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import ws from "loadtool/websocket";
import grpc from "loadtool/grpc";
import graphql from "loadtool/graphql";
import kafka from "loadtool/kafka";
import { check } from "loadtool";

// Five protocols in one iteration, two operations each, as one user
// journey against the demo API:
//
//   HTTP       GET  /api/me (the user)       POST /api/orders
//   WebSocket  connect, send                 receive
//   gRPC       Greeter.SayHello (unary)      Greeter.LotsOfReplies (stream)
//   GraphQL    query products                mutation placeOrder
//   Kafka      produce to "orders"           consume from "orders"
//
// Checks, thresholds, the console summary and the JSON and HTML reports
// cover all of them. The execution flow is in docs/mixed-protocols.md.
//
// Start the demo API (HTTP and WebSocket on 8090, gRPC on 8091, an
// in-process Kafka on 9092), then run:
//   go run ./examples/server
//   loadtool run examples/mixed-protocols.ts --vus 10 --duration 10s
//
// Checks compare with something only this VU sent where they can, so
// state leaking between VUs would fail them.

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const WS_URL = BASE_URL.replace(/^http/, "ws") + "/ws/echo";
const GRPC_ADDR = __ENV.GRPC_ADDR || "127.0.0.1:8091";
const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");

// Top-level code runs once in every VU: each VU gets its own gRPC client,
// GraphQL client, Kafka producer and Kafka consumer, none of which
// connects yet.
const grpcClient = new grpc.Client();
grpcClient.load(["proto"], "greeter.proto"); // relative to this script
const api = new graphql.Client(`${BASE_URL}/graphql`);
const orders = new kafka.Producer({ brokers: BROKERS, topic: "orders" });
// No group: each VU's consumer reads every partition, from the start.
const orderEvents = new kafka.Consumer({ brokers: BROKERS, topic: "orders", startAt: "earliest" });

// Per-VU state, kept across this VU's iterations.
let grpcConnected = false;

export const options = {
  thresholds: {
    checks: ["rate==1"],
    http_req_failed: ["rate==0"],
    ws_session_failed: ["rate==0"],
    grpc_req_failed: ["rate==0"],
    grpc_stream_failed: ["rate==0"],
    graphql_req_failed: ["rate==0"],
    kafka_produce_failed: ["rate==0"],
    kafka_consume_failed: ["rate==0"],
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
  const productId = data.productIds[__ITER % data.productIds.length];
  const json = { "Content-Type": "application/json" };

  // HTTP: log in, read the user back with the token, place an order.
  const login = http.post(`${BASE_URL}/api/login`, JSON.stringify({ username: user, password: "demo" }), {
    headers: json,
    responseType: "text",
  });
  const token = login.status === 200 ? (login.json() as { token: string }).token : "";
  const auth = { Authorization: `Bearer ${token}` };
  const me = http.get(`${BASE_URL}/api/me`, { headers: auth, responseType: "text" });
  check(me, { "http GET: this VU's user": (r) => r.status === 200 && (r.json() as { username: string }).username === user });
  const order = http.post(`${BASE_URL}/api/orders`, JSON.stringify({ productId, quantity: 1 }), {
    headers: { ...json, ...auth },
    responseType: "text",
  });
  check(order, { "http POST: order created": (r) => r.status === 201 && (r.json() as { productId: number }).productId === productId });

  // GraphQL, over the same HTTP connections, with the same token.
  const products = api.query("{ products { id name } }", { headers: auth });
  check(products, { "graphql query: products listed": (r) => r.ok && r.data.products.length === data.productIds.length });
  const placed = api.mutation(
    "mutation ($id: Int!, $qty: Int!) { placeOrder(productId: $id, quantity: $qty) { productId quantity } }",
    { variables: { id: productId, qty: 2 }, headers: auth },
  );
  check(placed, { "graphql mutation: order placed": (r) => r.ok && r.data.placeOrder.productId === productId });

  // WebSocket: connect, send, and receive the echo on this iteration's socket.
  const socket = ws.connect(WS_URL);
  socket.send(tag);
  const echo = socket.receive(5000);
  socket.close();
  check(echo, { "websocket: echoed this VU's message": (m) => m === tag });

  // gRPC: connect once per VU, then a unary call and a server stream on
  // the kept connection.
  if (!grpcConnected) {
    const conn = grpcClient.connect(GRPC_ADDR, { plaintext: true });
    grpcConnected = conn.error === "";
  }
  const hello = grpcClient.invoke("greeter.Greeter/SayHello", { name: user }, { timeout: "2s" });
  check(hello, { "grpc unary: greeted this VU": (r) => r.status === 0 && r.message.message === `Hello, ${user}` });
  const replies = grpcClient.stream("greeter.Greeter/LotsOfReplies", { timeout: "2s" });
  replies.send({ name: user, count: 3 });
  replies.closeSend();
  let mine = 0;
  for (let m = replies.recv(); m !== null; m = replies.recv()) {
    if (m.message.endsWith(`, ${user}`)) mine++;
  }
  check(replies, { "grpc stream: 3 replies to this VU": (s) => s.status === 0 && mine === 3 });

  // Kafka: publish the order, keyed by user, then read orders back.
  const sent = orders.produce({ key: user, value: JSON.stringify({ user, iter: __ITER, productId }) });
  check(sent, { "kafka produce: order acknowledged": (r) => r.ok && r.offset >= 0 });
  const received = orderEvents.consume({ max: 10, timeout: "1s" });
  check(orderEvents, { "kafka consume: orders read": (c) => c.error === "" && received.length > 0 });
}

// teardown runs once, after the VUs, with setup's data.
export function teardown(data: { productIds: number[] }) {
  console.log(`mixed-protocols: done (${data.productIds.length} products)`);
}
