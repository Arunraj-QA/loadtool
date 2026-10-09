// GraphQL benchmark (benchmarks/protocol.ps1): one query with a variable
// per iteration against the demo API's /graphql, which waits -delay
// before answering; no think time.
import graphql from "loadtool/graphql";

const api = new graphql.Client((__ENV.BASE_URL || "http://127.0.0.1:8090") + "/graphql");
const QUERY = "query ($id: Int!) { product(id: $id) { id name priceCents } }";

export default function () {
  const res = api.query(QUERY, { variables: { id: 1 + (__ITER % 5) } });
  if (!res.ok) throw new Error(res.error);
}
