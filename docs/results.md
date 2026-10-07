# Results

LoadTool prints a summary at the end of every run. It can also write the
same result as [JSON](json-summary.md) (`--summary-json`) and as an HTML
report with charts (`--report-html`).

```
LoadTool summary

  Script:      examples/thresholds.ts
  VUs:         10
  Duration:    10s (elapsed 10.01s)
  Status:      completed

  Requests:    9,245 (923.8 req/s)
  Success:     9,245
  Errors:      0 (0.00%)
  Script errs: 0

  Checks:      9,245 / 9,245 passed (100.00%)
    ✓ status is 200  9,245 / 9,245  100.00%

  Thresholds:  4 of 4 passed
    ✓ checks             rate>0.99  observed 1.0000 (100.00%)
    ✓ http_req_duration  p(95)<200  observed 11.60ms
    ✓ http_req_duration  p(99)<500  observed 11.99ms
    ✓ http_req_failed    rate<0.01  observed 0.0000 (0.00%)

  Latency (all requests sent, failed included):
    min      10.01ms
    mean     10.80ms
    p50      10.68ms
    p90      11.34ms
    p95      11.60ms
    p99      11.99ms
    max      19.48ms
  Latency (successful requests):
    p50      10.68ms
    p90      11.34ms
    p95      11.60ms
    p99      11.99ms
```

This is a real run of `examples/thresholds.ts` against the local
benchmark server (which answers after 10 ms).

## What is counted

**Requests:**

- A request **succeeds** when it gets a 2xx or 3xx response; anything else
  is an **error**, including network failures (status 0).
- A request that could not even be sent (an invalid URL) counts as an
  error but has no latency.
- Requests in `setup` and `teardown` are **not** counted.

**Script errors** are iterations that threw. The first message is shown.

**Iterations** count when they run to their end, script errors included.
Iterations cut off at the end of the test, or by Ctrl+C, do not count;
neither do their running requests.

**Dropped iterations** are arrival-rate starts that found no free VU,
shown only when there are some.

**Protocols** counts responses by HTTP version. The summary shows it when
any response used HTTP/2.

## Latency

The summary shows latency twice:

- **All requests sent**, failed ones included. This is the population
  k6's `http_req_duration` and JMeter use, and what thresholds on
  `http_req_duration` read.
- **Successful requests only.** Fast failures, such as refused
  connections, pull the first set down; this one is unaffected.

**What a request's latency covers:** from sending the request until the
whole response body is read, including connecting when a new connection
is needed.

**Precision:**

- Percentiles come from a fixed-size histogram and are within ±0.78 % of
  the exact value. Memory therefore does not grow with test length or
  request rate.
- Min, max, mean and counts are exact.

**Clock resolution.** On Windows, timings have a resolution of about
0.5 ms, so very fast responses can show as 0.

## Over time

The HTML report (and the JSON summary's `series`) has one point per
second:

| Value | Meaning |
|---|---|
| Requests and failures per second | Requests that completed in that second |
| Latency p50/p95/p99 | Of the requests that completed in that second |
| Active VUs | VUs the scenarios had active at that moment |

A second without requests is a gap in the latency lines, not a zero.
