# Getting started

This walks through installing LoadTool, running a first test against a
local server, and turning it into a pass/fail check.

## 1. Install

**From source** (works today). Install Go (the version in `go.mod`),
then:

```bash
git clone https://github.com/Arunraj-QA/loadtool.git
cd loadtool
go build -o bin/loadtool ./cmd/loadtool
./bin/loadtool --version
```

**From a release.** Released versions are static binaries for Linux,
macOS and Windows on the
[releases page](https://github.com/Arunraj-QA/loadtool/releases):
download, check against `checksums.txt`, unpack, run. No release has been
published yet.

## 2. Start something to test

Never point a load test at a server you do not own. The repository
includes a small demo API to practise on: a shop with products, orders
and a login. Every request takes about 5 ms.

```bash
go run ./examples/server
```

It listens on `127.0.0.1:8090`. Leave it running in its own terminal.

| Endpoint | What it does |
|---|---|
| `GET /api/products` | The product list |
| `GET /api/products/{id}` | One product |
| `POST /api/orders` | Creates an order from `{"productId": 1, "quantity": 2}` |
| `POST /api/login` | `{"username": "…", "password": "demo"}`: a token and a session cookie |
| `GET /api/me` | The logged-in user |

## 3. Write and run a test

Save this as `first.ts`:

```typescript
import http from "loadtool/http";

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export default function () {
  http.get(`${BASE_URL}/api/products`);
}
```

Run it with 10 virtual users for 10 seconds:

```bash
./bin/loadtool run first.ts --vus 10 --duration 10s
```

Each VU (virtual user) calls the default function in a loop. The summary
shows requests, errors and latency percentiles; [Results](results.md)
explains each line.

`BASE_URL` comes from the environment, so the same script can test
another server: `./bin/loadtool run -e BASE_URL=https://staging.example.test first.ts`.

## 4. Check responses

A request "succeeds" when it gets a 2xx or 3xx status. To check more
than that, use `check`:

```typescript
import http from "loadtool/http";
import { check } from "loadtool";

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8090";

export default function () {
  // Response bodies are discarded by default to save memory;
  // responseType "text" keeps this one so the check can read it.
  const res = http.get(`${BASE_URL}/api/products`, { responseType: "text" });
  check(res, {
    "status is 200": (r) => r.status === 200,
    "has products": (r) => r.json().products.length > 0,
  });
}
```

The summary lists each check's pass rate. A failed check does not stop
the test.

## 5. Make it pass or fail

Thresholds turn the test into a verdict. Add them to the script:

```typescript
export const options = {
  vus: 10,
  duration: "10s",
  thresholds: {
    http_req_duration: ["p(95)<200"], // 95% of requests under 200 ms
    http_req_failed: ["rate<0.01"],   // under 1% errors
    checks: ["rate>0.99"],
  },
};
```

Run it again without `--vus`/`--duration`, since the script now sets
them. The exit code is 0 if every threshold passed and 99 if one failed.
That is all a CI job needs ([CI guide](ci/README.md)).

## 6. Keep the results

```bash
./bin/loadtool run first.ts --out json=summary.json --report-html report.html
```

**The two files:**

- `report.html` is a single file with charts over time; open it in any
  browser.
- `summary.json` is for tools ([format](json-summary.md)).

## Next

- **Ready-made scripts for every feature:** [Examples](../examples/README.md):
  POST JSON, logins with tokens or cookies, scenarios, HTTP/2. They all
  run against the demo API.
- **More script features:** [Script API](script-api.md), including
  setup/teardown, cookies, imports and HTTP/2.
- **Workload shapes:** ramping and arrival-rate
  [scenarios](options.md#scenarios).
- **Coming from k6:** [Differences from k6](k6-differences.md).
- **Something not working:** [Troubleshooting](troubleshooting.md).
