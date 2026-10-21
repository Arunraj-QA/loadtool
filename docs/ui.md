# Testing one API from the browser (`loadtool ui`)

`loadtool ui` load-tests **one HTTP API** from a page in your browser,
without writing a script. The page only configures the test and shows
results. LoadTool runs the test with the same engine, HTTP client and
reports as `loadtool run`.

```bash
loadtool ui
# LoadTool UI: http://127.0.0.1:53817/#token=4f1c…
```

Open the printed URL. It is only on this machine (`127.0.0.1`), and the
part after `#token=` is a key for this session: anyone without it
cannot start tests. Stop the UI with Ctrl+C.

> **Only test APIs you are authorized to load-test.** The load comes
> from your machine and your network address.

## Configuring a test

| Field | Notes |
|---|---|
| Method and URL | `GET`, `POST`, `PUT`, `PATCH` or `DELETE`, and an absolute `http://` or `https://` URL |
| Query parameters | Added to the URL, encoded |
| Headers | Name and value. **Secret** masks the value (see below) |
| Authentication | None, a bearer token, or basic (user name and password) |
| JSON body | For `POST`, `PUT`, `PATCH` and `DELETE`; must be valid JSON. `Content-Type: application/json` is set unless you set it |
| Virtual users, duration | Up to 50 VUs and 5 minutes by default (see the flags) |
| Expected status | The range of status codes that count as a passed check (default 200–299) |
| p95 threshold, max error rate | Optional thresholds, evaluated as in a script |
| Authorization | You must confirm you may load-test the API |

**Send one request** sends the request once, records nothing, and shows
the response:
- status, time and protocol;
- headers;
- the first 2 KB of the body.

**Start test** runs it, and **Cancel** stops it. One test runs at a time.

## Results

- **While it runs:**
  - elapsed time, active and configured VUs, requests, failed requests
    and error rate so far;
  - requests per second and latency for the **latest second**, from the
    engine's time series.
- **When it ends:**
  - every number comes from the run's **JSON report**: total requests,
    requests per second, average and p50, p90, p95 and p99 over the
    whole run, failed requests and error rate;
  - status codes, the check, and the thresholds.
- **Charts:** requests per second and p95 latency, per second.
- **Downloads:** the JSON and HTML reports, the same as `loadtool run`
  writes (`--out json=…`, `--report-html`). Their script field shows the
  request, such as `GET https://api.example.com/v1/items`.

The page calculates no metrics of its own. Percentiles are LoadTool's
(within ±0.78 %, as in every report).

A test ends as **completed**, **cancelled** (partial results are kept)
or **failed** (LoadTool could not run it; the reason is shown).

## Safety

- **Public addresses only.** The URL's host must be a public internet
  address. LoadTool refuses:
  - loopback and `localhost`;
  - private networks (10/8, 172.16/12, 192.168/16, and IPv6 unique-local);
  - link-local addresses, including cloud metadata (`169.254.169.254`);
  - shared, reserved, documentation and multicast ranges.

  The check runs again **on every connection**, on the address the host
  name resolves to, so a name that resolves to a private address is
  refused too.
- **No proxy.** Proxy environment variables are ignored, so connections
  go straight to the API.
- **No redirects.** Redirects are never followed: a 3xx response is the
  result.
- **Secrets stay private.** Secret header and query values, bearer
  tokens and basic-auth passwords are masked on the page. They are
  replaced with `***` in everything LoadTool shows or saves: responses,
  errors, reports and its log. They are sent only to the API.
- **Only this machine can drive it.** The UI checks the `Host` and
  `Origin` of every request, and requires the session token, so other
  websites open in your browser cannot start tests.
- **Every request has a timeout:** 10 s by default.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--port` | `0` | Port on 127.0.0.1; `0` picks a free one |
| `--max-vus` | `50` | The most VUs a test may use |
| `--max-duration` | `5m` | The longest a test may run |
| `--request-timeout` | `10s` | Timeout of each request |
| `--allow-private-targets` | off | Allow loopback and private destinations, for local servers such as the [demo API](../examples/README.md) |

## Compared with a script

The UI covers one request, repeated. For several steps, logins, data
from files, scenarios or other protocols, write a script and use
`loadtool run` ([Script API](script-api.md)).

The design is in [ADR-023](decisions/ADR-023-local-ui-controller.md).
