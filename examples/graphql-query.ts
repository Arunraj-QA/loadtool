/// <reference path="../types/loadtool.d.ts" />

import graphql from "loadtool/graphql";
import { check } from "loadtool";

// GraphQL queries with variables, over LoadTool's HTTP transport (the same
// connections, HTTP/2 and cookies as loadtool/http).
//
// Start the demo API (it serves GraphQL at /graphql), then run:
//   go run ./examples/server
//   loadtool run examples/graphql-query.ts --vus 10 --duration 10s

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";
const api = new graphql.Client(`${BASE_URL}/graphql`, {
  headers: { "X-Client": "loadtool-example" }, // sent with every operation
});

const PRODUCT = `
  query Product($id: Int!) {
    product(id: $id) { id name priceCents }
  }`;

export const options = {
  thresholds: {
    graphql_req_failed: ["rate<0.01"],    // fewer than 1% fail (HTTP or GraphQL)
    graphql_req_duration: ["p(95)<200"],
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  // A query without variables.
  const list = api.query("{ products { id name } }");
  check(list, {
    "list: ok": (r) => r.ok,
    "list: five products": (r) => r.data.products.length === 5,
  });

  // A query with variables, picked per VU and iteration.
  const id = 1 + ((__VU + __ITER) % 5);
  const one = api.query(PRODUCT, { variables: { id }, operationName: "Product" });
  check(one, {
    "product: HTTP 200": (r) => r.http_ok && r.status === 200,
    "product: no GraphQL errors": (r) => r.errors.length === 0,
    "product: right one": (r) => r.data.product.id === id,
  });
}
