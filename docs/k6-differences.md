# Differences from k6

LoadTool's script API follows the shape of [k6](https://k6.io)'s, so k6
users can read and write LoadTool tests without relearning. It is a
separate implementation, a subset of k6, not a fork: no k6 code is used.
This page lists what to change when porting a k6 script, and where
behaviour differs on purpose.

## Porting a script

| k6 | LoadTool |
|---|---|
| `import http from "k6/http"` | `import http from "loadtool/http"` |
| `import { check, sleep, group } from "k6"` | `import { check, sleep, group } from "loadtool"` |
| `k6 run script.js` | `loadtool run script.js` |
| `k6 run -e KEY=value` | `loadtool run -e KEY=value` |
| `--summary-export file.json` | `--summary-json file.json` (a different format: [JSON summary](json-summary.md)) |

Most scripts that use `http.get/post/put/patch/del/request`, `check`,
`sleep`, `options` with `vus`/`duration`/`stages`/`scenarios`/`thresholds`,
and `setup`/`teardown` need only the import lines changed.

## Supported, with differences

| Area | Difference |
|---|---|
| **Redirects** | Never followed; a 3xx is returned and counts as a success. k6 follows up to 10. |
| **Request bodies** | Strings only. k6 form-encodes an object; LoadTool throws a `TypeError` (use `JSON.stringify`). |
| **Request params** | `headers` and `cookies` only. Others (`timeout`, `tags`, `redirects`, `responseType`, …) warn and are ignored. The request timeout is 30 s. |
| **Responses** | `status`, `proto`, `error`, `headers`, `body`, `json()`, `timings.duration`, `url`, `cookies`. No `html()`, no `json(selector)`, no detailed timings (`waiting`, `connecting`, …). |
| **Latency** | `timings.duration` and `http_req_duration` include connecting when a new connection is made; k6's `http_req_duration` excludes it. With keep-alive (the default) this only affects first requests. |
| **`setup` / `teardown` requests** | Not counted in results or thresholds. k6 counts them. |
| **`check`** | A condition that throws counts as a failed check; the iteration goes on. |
| **`group`** | Runs the function; results are not broken down by group. |
| **Thresholds** | Metrics: `http_req_duration`, `http_req_failed`, `http_reqs`, `checks`, `iterations`, `dropped_iterations`. A threshold with no data **fails**. Exit code 99 on failure, as in k6. |
| **Executors** | `constant-vus`, `ramping-vus`, `constant-arrival-rate`. `maxVUs` must equal `preAllocatedVUs`: all VUs are created up front. |
| **Ramping** | VU *n* is active while the stage line is at least *n*: a pure ramp to 10 reaches the 10th VU only at its end. |
| **Options** | Unknown options warn and are ignored. Options combining `scenarios` with `vus`/`duration`/`stages` are an error. |
| **HTTP/2** | Negotiated over TLS by default, as in k6. `httpVersion: "2"` also speaks h2c (HTTP/2 without TLS), which k6 does not. |
| **Percentiles** | From a histogram, within ±0.78 %. |

## Not supported (yet)

**Script API:**

- `open()`: import JSON files instead (`import data from "./data.json"`).
- `SharedArray`.
- `http.batch`.
- `http.cookieJar()`.
- File uploads, form-encoded bodies.

**Metrics and executors:**

- Custom metrics (`Counter`, `Trend`, `Rate`, `Gauge`).
- Tags and sub-metric thresholds.
- `abortOnFail`.
- The other executors: `shared-iterations`, `per-vu-iterations`,
  `ramping-arrival-rate`, `constant-arrival-rate` with growing `maxVUs`,
  `externally-controlled`.

**Runtime and modules:**

- Other protocols (WebSocket, gRPC) and browser testing.
- npm packages and remote (URL) modules.
- `--out` streaming outputs and k6 Cloud.
- `insecureSkipTLSVerify` and other TLS options.

**Unsupported options** produce a warning, so a ported script tells you
what it relied on.
