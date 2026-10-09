/// <reference path="../types/loadtool.d.ts" />

import http from "loadtool/http";
import graphql from "loadtool/graphql";
import { check } from "loadtool";

// A GraphQL mutation with variables, after an HTTP login: the bearer
// token goes in the request headers.
//
// Start the demo API, then run:
//   go run ./examples/server
//   loadtool run examples/graphql-mutation.ts --vus 5 --duration 10s

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

const PLACE_ORDER = `
  mutation PlaceOrder($productId: Int!, $quantity: Int!) {
    placeOrder(productId: $productId, quantity: $quantity) {
      id productId quantity totalCents
    }
  }`;

export const options = {
  thresholds: {
    graphql_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  const login = http.post(
    `${BASE_URL}/api/login`,
    JSON.stringify({ username: `gql-${__VU}`, password: __ENV.API_PASSWORD || "demo" }),
    { headers: { "Content-Type": "application/json" }, responseType: "text" },
  );
  const token = login.json().token;

  const quantity = 1 + (__ITER % 3);
  const res = graphql.mutation(`${BASE_URL}/graphql`, PLACE_ORDER, {
    variables: { productId: 2, quantity },
    headers: { Authorization: `Bearer ${token}` },
  });

  check(res, {
    "order: ok": (r) => r.ok,
    "order: quantity echoed": (r) => r.data.placeOrder.quantity === quantity,
    "order: total": (r) => r.data.placeOrder.totalCents === 3900 * quantity,
  });
}
