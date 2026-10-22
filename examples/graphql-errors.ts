/// <reference path="../types/loadtool.d.ts" />

import graphql from "loadtool/graphql";
import { check } from "loadtool";

// GraphQL errors, apart from transport errors. A GraphQL server usually
// answers HTTP 200 even when the operation failed, with the reasons in
// "errors": http_ok is then true and ok false.
//
// Start the demo API, then run:
//   go run ./examples/server
//   loadtool run examples/graphql-errors.ts --vus 2 --duration 5s
//
// Every operation here fails on purpose, so graphql_req_failed is 100%.
// The checks pass: they assert that each failure is reported correctly.

const URL = (__ENV.BASE_URL || "http://127.0.0.1:8090") + "/graphql";

export const options = {
  thresholds: {
    graphql_errors: ["count>0"], // GraphQL errors were seen (and counted)
    checks: ["rate==1"],
  },
};

export default function (): void {
  // HTTP 200 with a GraphQL error: an unknown product. Partial data is
  // still there: products resolved, product is null.
  const partial = graphql.query(URL, "{ product(id: 999) { name } products { id } }");
  check(partial, {
    "partial: HTTP succeeded": (r) => r.http_ok && r.status === 200,
    "partial: GraphQL failed": (r) => !r.ok && r.error_code === "server",
    "partial: the error message": (r) => r.errors[0].message === "no product with id 999",
    "partial: the data that resolved": (r) => r.data.product === null && r.data.products.length === 5,
  });

  // A mutation rejected by validation in the resolver.
  const rejected = graphql.mutation(URL, "mutation { placeOrder(productId: 1, quantity: 0) { id } }");
  check(rejected, {
    "rejected: GraphQL error": (r) => !r.ok && r.errors.length === 1,
    "rejected: the reason": (r) => r.error === "quantity must be 1 to 100",
  });

  // An invalid document: the server cannot run it at all.
  const invalid = graphql.query(URL, "{ noSuchField }");
  check(invalid, { "invalid: a validation error": (r) => !r.ok && r.errors.length > 0 });
}
