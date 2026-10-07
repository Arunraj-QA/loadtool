import http from "loadtool/http";
import { check } from "loadtool";

// A small client for the API under test, shared by test scripts. Files
// imported with a relative path are bundled into the test.

export interface Product {
  id: number;
  name: string;
}

// getProduct fetches one product and checks it is the expected one.
export function getProduct(baseURL: string, product: Product): void {
  const res = http.get(`${baseURL}/api/products/${product.id}`, {
    headers: { Accept: "application/json" },
    responseType: "text", // keep the body for the checks (discarded by default)
  });
  check(res, {
    "product: status is 200": (r) => r.status === 200,
    "product: expected name": (r) => r.json().name === product.name,
  });
}
