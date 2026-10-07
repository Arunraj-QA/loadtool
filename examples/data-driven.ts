/// <reference path="../types/loadtool.d.ts" />

import { sleep } from "loadtool";
import { getProduct, Product } from "./lib/api.ts";
import products from "./data/products.json";

// Data-driven test: test data from a JSON file, request code in a shared
// module. Both are imported with relative paths and bundled into the test.
//
// Start the demo API (examples/server), then run:
//   go run ./examples/server
//   loadtool run examples/data-driven.ts --vus 4 --duration 10s

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export const options = {
  thresholds: { checks: ["rate>0.99"] },
};

// Each VU walks the list from its own starting point, so VUs request
// different products at the same time.
export default function (): void {
  const product: Product = products[(__VU + __ITER) % products.length];
  getProduct(BASE_URL, product);
  sleep(0.2);
}
