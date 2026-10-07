# Examples

Every example reads `BASE_URL` from the environment, so it can point at
any server: `loadtool run -e BASE_URL=https://staging.example.test ...`.

The ones marked **local** run against the repository's benchmark server
as they are, and CI runs them on every change (`scripts/smoke-examples.sh`):

```bash
go run ./benchmarks/server        # in another terminal; 127.0.0.1:8080
loadtool run examples/checks.ts --vus 5 --duration 10s
```

| Example | Shows | Target |
|---|---|---|
| [`basic-http.ts`](basic-http.ts) | The smallest test: a GET per iteration, fail on a bad status | local |
| [`basic-http.js`](basic-http.js) | The same in plain JavaScript | local |
| [`checks.ts`](checks.ts) | `check`, response headers and `json()`, `sleep` | local |
| [`thresholds.ts`](thresholds.ts) | Pass/fail criteria and exit code 99 | local |
| [`lifecycle.ts`](lifecycle.ts) | `setup` (fail fast if the target is down), data for every iteration, `teardown` | local |
| [`data-driven.ts`](data-driven.ts) | Test data from a JSON file and a shared module ([`lib/api.ts`](lib/api.ts), [`data/products.json`](data/products.json)) | local |
| [`scenarios.ts`](scenarios.ts) | Two scenarios at once: ramping VUs with think time and a constant arrival rate; `exec` functions | local (20 s) |
| [`sessions.ts`](sessions.ts) | A login cookie kept for the iteration; `res.cookies` | your app with a cookie login |
| [`http2.ts`](http2.ts) | Requiring HTTP/2 with `httpVersion: "2"`, checking `res.proto` | an HTTPS server with HTTP/2 |

**Editor support.** Each example starts with a reference to
[`types/loadtool.d.ts`](../types/loadtool.d.ts), which gives editors
completion and type hints. LoadTool itself does not type-check.

**Example report.** [`reports/example-report.html`](reports/example-report.html)
shows what `--report-html` writes: a failed run with an error burst. It
uses sample data, not a measurement; open the file in a browser.
