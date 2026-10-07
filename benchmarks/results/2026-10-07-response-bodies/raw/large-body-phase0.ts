// The large-response scenario for the Phase 0 build (3b7b523), which has a
// global http object and no imports or __ENV. Same request as
// benchmarks/loadtool/large-body.ts.
const TARGET = "http://127.0.0.1:8080/api/large";

export default function (): void {
  const res = http.get(TARGET);
  if (res.status !== 200) {
    throw new Error(`status ${res.status} ${res.error}`);
  }
}
