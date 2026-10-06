# Command line

```
loadtool run [flags] <script>
loadtool --version
loadtool --help
```

## `loadtool run`

| Flag | Default | Meaning |
|---|---|---|
| `-u, --vus` | `1` | VUs. Overrides the script and `LOADTOOL_VUS`, and replaces the script's scenarios. |
| `-d, --duration` | `10s` | How long VUs start iterations. Overrides the script and `LOADTOOL_DURATION`, and replaces the script's scenarios. |
| `--graceful-stop` | `30s` | How long running iterations may finish after the duration; `0` cancels them at once. Applies to the `vus`/`duration` shorthand. |
| `-e, --env` | | `KEY=VALUE` for the script's `__ENV`; repeatable; overrides the process environment |
| `--summary-json` | | Also write the summary as JSON to this file ([format](json-summary.md)) |
| `--report-html` | | Also write a self-contained HTML report with charts to this file |

**`--vus` and `--duration`** count only when typed: the defaults shown
apply when nothing else sets a value (see
[Options](options.md#where-settings-come-from)).

## Environment variables

| Variable | Meaning |
|---|---|
| `LOADTOOL_VUS`, `LOADTOOL_DURATION` | Like `--vus` and `--duration`, below typed flags |
| Any other | Visible to the script as `__ENV.NAME` |

## Output

| Stream | Content |
|---|---|
| stdout | The summary (see [Results](results.md)) |
| stderr | Script `console` output, warnings, errors |

**Result files.** `--summary-json` and `--report-html` are written
whenever the test produced a result, also when thresholds fail. They are
written atomically: a reader never sees half a file.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The test ran and every threshold passed |
| `99` | The test ran, and at least one threshold failed |
| `1` | Anything else, in two groups below |

Exit code 1 covers two kinds of failure:

- **The test could not start:** script or option errors, a setup failure.
- **The test ran but did not finish cleanly:** interrupted, failed
  teardown, or a result file that could not be written.

## Ctrl+C

**One Ctrl+C** stops starting new iterations and cancels running
requests. Teardown still runs, then a partial summary is printed (marked
"interrupted") and the exit code is 1.

**A second Ctrl+C** exits at once.
