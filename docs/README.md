# LoadTool documentation

## Using LoadTool

| Page | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install, a first test, checks, thresholds, result files |
| [Script API](script-api.md) | `loadtool/http`, responses, `check`/`sleep`/`group`, globals, imports, setup/teardown, cookies, HTTP/2 |
| [Options](options.md) | Every option, where settings come from, scenarios and executors, thresholds |
| [Command line](cli.md) | Flags, environment variables, output, exit codes, Ctrl+C |
| [Results](results.md) | The summary, what is counted, latency and precision, the time series |
| [JSON summary](json-summary.md) | `--summary-json` format and versioning |
| [CI](ci/README.md) | GitHub Action, GitLab CI, Jenkins, Azure Pipelines |
| [Differences from k6](k6-differences.md) | Porting k6 scripts; what differs and what is missing |
| [Examples](../examples/README.md) | Runnable example scripts |

## Working on LoadTool

| Page | What it covers |
|---|---|
| [Contributing](../CONTRIBUTING.md) | Building, testing, the workflow for changes |
| [Architecture](architecture.md) | Components, data flow, Phase 1 build order |
| [Workflows](workflows.md) | Diagrams of a test run, development and benchmarking |
| [Decisions](decisions/README.md) | Architecture decision records (ADR-001 onward) |
| [Benchmarks](../benchmarks/README.md) | How LoadTool is measured against k6 and JMeter, and the results |
| [Releasing](releasing.md) | Cutting a release |
