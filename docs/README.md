# LoadTool documentation

## Using LoadTool

| Page | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install, a first test, checks, thresholds, result files |
| [Script API](script-api.md) | `loadtool/http`, responses, `check`/`sleep`/`group`, globals, imports, setup/teardown, cookies, HTTP/2, WebSocket, gRPC, GraphQL, Kafka |
| [Options](options.md) | Every option, where settings come from, scenarios and executors, thresholds |
| [Command line](cli.md) | Flags, environment variables, output, exit codes, Ctrl+C |
| [Results](results.md) | The summary, what is counted, latency and precision, the time series |
| [JSON summary](json-summary.md) | `--out json` format, schema and versioning |
| [CI](ci/README.md) | GitHub Action, the generic recipe, GitLab CI, Jenkins, Azure Pipelines |
| [Troubleshooting](troubleshooting.md) | Common problems and the messages LoadTool prints |
| [Differences from k6](k6-differences.md) | Porting k6 scripts; what differs and what is missing |
| [Examples](../examples/README.md) | Ten runnable scripts and the demo API they run against |

**Find a topic:**

| Topic | Where |
|---|---|
| Installation, a first test | [Getting started](getting-started.md) |
| The test script (DSL) | [Script API](script-api.md#a-test-script) |
| HTTP requests and responses | [Script API: `loadtool/http`](script-api.md#loadtoolhttp) |
| Checks | [Script API: `check`](script-api.md#checkvalue-conditions) |
| Thresholds | [Options: Thresholds](options.md#thresholds) |
| Scenarios (constant VUs, ramping, arrival rate) | [Options: Scenarios](options.md#scenarios) |
| Setup and teardown | [Script API: Lifecycle](script-api.md#lifecycle) |
| Cookies and sessions | [Script API: Cookies and sessions](script-api.md#cookies-and-sessions) |
| HTTP/2 | [Script API: HTTP versions](script-api.md#http-versions-and-connections) |
| WebSocket | [Script API: `loadtool/ws`](script-api.md#loadtoolws) |
| gRPC | [Script API: `loadtool/grpc`](script-api.md#loadtoolgrpc) |
| GraphQL | [Script API: `loadtool/graphql`](script-api.md#loadtoolgraphql) |
| Kafka | [Script API: `loadtool/kafka`](script-api.md#loadtoolkafka) |
| Several protocols in one test | [Mixed-protocol tests](mixed-protocols.md) |
| JSON reports | [JSON summary](json-summary.md) |
| HTML reports | [Results: The HTML report](results.md#the-html-report) |
| CI usage | [CI](ci/README.md) |
| Troubleshooting | [Troubleshooting](troubleshooting.md) |

## Working on LoadTool

| Page | What it covers |
|---|---|
| [Contributing](../CONTRIBUTING.md) | Building, testing, the workflow for changes |
| [Architecture](architecture.md) | Components, data flow, Phase 1 build order |
| [Workflows](workflows.md) | Diagrams of a test run, development and benchmarking |
| [Decisions](decisions/README.md) | Architecture decision records (ADR-001 onward) |
| [Benchmarks](../benchmarks/README.md) | How LoadTool is measured against k6 and JMeter, and the results |
| [Releasing](releasing.md) | Cutting a release |
