import http from "loadtool/http";
import { check } from "loadtool";

// A small client for the API under test, shared by test scripts. Files
// imported with a relative path are bundled into the test.

export interface Product {
  id: number;
  name: string;
}

export function getProduct(baseURL: string, product: Product): void {
  const res = http.get(`${baseURL}/api/test?id=${product.id}`, {
    headers: { Accept: "application/json" },
  });
  check(res, {
    "product: status is 200": (r) => r.status === 200,
    "product: JSON body": (r) => r.json().status === "ok",
  });
}
