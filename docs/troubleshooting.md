# Troubleshooting

Problems first-time users run into, with the messages LoadTool prints.
Each section names the symptom, the cause and the fix.

**A good first step for any request problem:** print what the first
iteration gets back.

```typescript
const res = http.get(url);
if (__ITER === 0) console.log(res.status, res.error, res.body);
```

## The test does not start

LoadTool checks the script, options and thresholds before generating any
load. A problem stops the run with `Error: …` and exit code 1, and no
result files are written.

| Message (start) | Cause | Fix |
|---|---|---|
| `load script: …: cannot import "axios": only built-in modules (…) and relative paths …` | npm packages and URLs cannot be imported | Use `loadtool`, `loadtool/http`, `loadtool/ws` and your own files (`./lib/api.ts`) |
| `script init: … http requests are not allowed in the script's top-level code` | A request outside a function | Move it into the default function, or into `setup()` to run it once |
| `options.thresholds: unknown metric "http_req_duraton"; supported metrics are …` | A typo in a metric name | Use a name from the list in the message |
| `options.thresholds.http_req_duration: "p95<200": expected "<aggregate> <op> <number>"` | A malformed expression | Write `p(95)<200`; see [Thresholds](options.md#thresholds) |
| `setup: Error: …` | `setup()` threw, for example a failed login | Read the message; setup failing on purpose stops a test against a broken target |

**Scenario options** that are wrong are reported the same way, naming the
scenario and the field.

## Every request fails

The summary shows `Errors: … (100.00%)` and `Latency (successful
requests): none succeeded`.

**Status 0 with an `error`** means no response arrived:

| `error` contains | Cause | Fix |
|---|---|---|
| `connection refused` / `actively refused it` | Nothing listens at that address | Start the target; check `BASE_URL` and the port |
| `no such host` | The host name does not resolve | Check `BASE_URL` |
| `http2: … frame header looked like an HTTP/1.1 header` | `httpVersion: "2"` with an `http://` server that does not speak HTTP/2 without TLS | Remove `httpVersion` (the default `"auto"` uses HTTP/1.1 for `http://`) |
| `certificate` | The server's TLS certificate is not trusted | See [HTTPS certificates](#https-certificates) |
| `Client.Timeout exceeded` | No complete response within 30 s | The target is overloaded or unreachable |

**A status of 400 or more** is a response, but it counts as a failed
request. For example, a 401 means the request was not logged in: see
[Cookies and sessions](script-api.md#cookies-and-sessions).

## Some requests fail

- **`res.status` 4xx/5xx under load:** the target is rejecting or failing
  requests. Compare the error rate at a few VU counts.
- **Failed requests that are expected** (a test of a 404 page, say) still
  count in `http_req_failed`. Every status of 400 or more is a failed
  request. Leave the expected ones out of thresholded tests.

## "Script errs" in the summary

An iteration threw. The summary shows the count and the first message
(`first: …`). The test goes on with the next iteration.

| First message | Cause | Fix |
|---|---|---|
| `TypeError: http: the request body must be a string; use JSON.stringify(...)` | An object passed as a body | `http.post(url, JSON.stringify(obj), { headers: { "Content-Type": "application/json" } })` |
| `SyntaxError: …` from `json()` | The body is not JSON, such as an HTML error page | Check `status` first, or call `json()` inside a `check` |
| `the async function did not finish: it awaits something that never resolves …` | An `async` function awaits something that never resolves; LoadTool has no event loop | Remove the `await`: LoadTool calls (`http.get`, `ws.connect`, …) are blocking and return their result directly |
| `Error: …` from your code | A `throw` in the script | Expected, if the script throws on purpose (see `examples/basic-http.ts`) |

## Checks fail

The summary lists each check with its pass rate and the first error, such
as `first error: SyntaxError: …`.

- **`first error: TypeError: res.json(): the body was discarded`:** the
  body was not kept, which is the default. Add `responseType: "text"` to
  that request's params, or set `discardResponseBodies: false` in the
  options.
- **A condition that throws counts as a failed check.** `r.json().products`
  on an error page fails the check, not the iteration.
- **Check the status before the body:** a failed request makes every
  body check fail too.

## Warnings

| Warning | Meaning |
|---|---|
| `script option "insecureSkipTLSVerify" is not supported yet and was ignored` | An option LoadTool does not have, often from a k6 script; see [Differences from k6](k6-differences.md) |
| `res.body is null: response bodies are discarded by default; …` | The script read a body that was not kept. The warning says how to keep it. |
| `--vus/--duration (or LOADTOOL_VUS/LOADTOOL_DURATION) replace the script's scenarios or stages with one constant-vus scenario` | Typed `--vus`/`--duration` override the script's workload. Leave them out to run the script's scenarios. |

## Thresholds fail (exit code 99)

The summary marks each threshold `✓` or `✗`, with the observed value, and
the run exits with 99.

- **`no data`:** the metric has no values. For example, `checks` in a
  script that never calls `check` fails. Remove the threshold or add
  checks.
- **`≈` next to a value:** a percentile within ±0.78 % of its limit, the
  histogram's precision. A threshold this close to the limit can pass or
  fail between runs.
- **Exit code 1 instead of 99:** the run was interrupted or teardown
  failed, which wins over failed thresholds.

## "Dropped: N iterations"

A `constant-arrival-rate` scenario had no free VU when an iteration was
due, so the start was dropped and the rate was not reached. Raise the
scenario's `preAllocatedVUs`. `dropped_iterations: ["count==0"]` makes
this fail the test.

## WebSocket

| Symptom | Cause | Fix |
|---|---|---|
| `res.status` is 404 (or another HTTP status) and `error_code` is `server` | The server refused the upgrade at that path | Check the WebSocket path, such as `/ws/echo` |
| `error_code` `invalid` | The URL is not `ws://` or `wss://` | Use `ws://` (or `wss://` for TLS) |
| `ws.connect` never returns | Nothing closes the session | Close it in a handler, or add a guard: `socket.setTimeout(() => socket.close(), 5000)` |
| `ws_msg_latency` has no samples | In the callback style, no send was marked `{ reply: true }` | Mark the sends whose replies you want timed |
| `socket.receive()` returns `null`, `error_code` `timeout` | No message arrived within the timeout (default 30 s) | Check the server answers that message; raise the timeout if replies are slow |
| Warning `ws: a socket was still open when the iteration ended` | A blocking-style socket was not closed | Call `socket.close()`; LoadTool closed it for you |
| `on("error")` with `closed`, close code 1006 | The server dropped the connection without a close frame | Check the server's logs and limits |
| `thresholds … unknown metric "ws_…"` | The script does not import `loadtool/ws` | WebSocket metrics exist only in scripts that import it |

## gRPC

| Symptom | Cause | Fix |
|---|---|---|
| `client.load: … could not … greeter.proto` | The `.proto` file was not found | Paths are relative to the script; pass the directory as an import path: `client.load(["proto"], "greeter.proto")` |
| `connect` returns `error_code` `dial` | Nothing listens there, or TLS failed | Check the address; for a server without TLS add `plaintext: true` |
| `error_code` `invalid`: `unknown method` | No loaded `.proto` or reflection describes it | Load the right `.proto` file, or `connect(…, { reflect: true })`; write the method as `"package.Service/Method"` |
| `error_code` `invalid`: `not connected` | `invoke` before `connect`, or after a `connect` that failed | Connect until it succeeds: keep a `connected` flag per VU and connect while it is false (see `examples/grpc-unary.ts`). With `if (__ITER === 0)`, one failed connect leaves the VU unconnected for the whole test |
| `error_code` `timeout`, `status_text` `DeadlineExceeded` | The call took longer than its timeout (default 30 s) | Raise `timeout`, or look at the server |
| `is a streaming method; use client.stream` | `invoke` on a streaming method | Use `client.stream` |
| Warning `grpc: a stream was still open when the iteration ended` | A stream was not read to the end or closed | Read until `recv()` returns `null`, or call `stream.close()` |

## GraphQL

| Symptom | Cause | Fix |
|---|---|---|
| `http_ok` true but `ok` false, `error_code` `server` | The server answered HTTP 200 with GraphQL `errors` | Read `res.errors` (and `res.error`, the first message); `res.data` may hold partial data |
| `error_code` `protocol` | The response was not GraphQL JSON (an HTML error page, a wrong URL) | Check the endpoint URL |
| `graphql_reqs` counts but `http_reqs` does not | GraphQL operations are counted under `graphql_*` only | Use `graphql_req_failed` and `graphql_req_duration` in thresholds |

## Logins and sessions

- **Every iteration logs in again.** Each iteration starts with an empty
  cookie jar, like a new visitor. Set `noCookiesReset: true` to keep one
  session per VU.
- **A token from `setup()`** is shared by every VU: send it with
  `params.headers` (see `examples/auth-token.ts`).
- **An `__ENV` value you did not set.** `__ENV` includes the whole process
  environment. Names such as `USERNAME`, `USER`, `HOME` or `PATH` already
  have values, so a script reading `__ENV.USERNAME` gets the operating
  system's user. Use names of your own, such as `API_USER`.

## HTTPS certificates

Certificates are always verified, and there is no option to skip that.
`insecureSkipTLSVerify` is ignored with a warning.

To test a server with a self-signed or private certificate, add its
certificate authority to the operating system's trusted certificates. On
Linux, the `SSL_CERT_FILE` or `SSL_CERT_DIR` environment variables also
point LoadTool at extra certificates.

## Numbers that look wrong

- **Latency `0.00µs`:** on Windows, timings have a resolution of about
  0.5 ms, so very fast responses (and requests refused at once) show as 0.
- **No charts in the HTML report:** charts need at least two per-second
  points, so a run of about two seconds or more.
- **`res.body` is `null`:** response bodies are discarded by default (a
  warning says so), or no response arrived. See the next section.

## Result files are missing

`--out json=<file>` and `--report-html` are written whenever the test
produced a result, also when thresholds fail. They are **not** written
when the test could not start (exit code 1 with an `Error:` line); fix
the error first.

## Still stuck

- **Search the issues** on GitHub. If nothing matches, open one with:
  - the LoadTool version (`loadtool --version`);
  - the command;
  - the full output;
  - a script that shows the problem against the demo API
    (`go run ./examples/server`), if you can.
- **Security problems:** see [`SECURITY.md`](../SECURITY.md).
