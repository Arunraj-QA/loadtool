# LoadTool documentation

## Using LoadTool

| Page | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install, a first test, checks, thresholds, result files |
| [Testing one API from the browser](ui.md) | `loadtool ui`: configure, run and view a test of one HTTP API without a script |
| [Script API](script-api.md) | A test script, `check`/`sleep`/`group`, globals, imports, setup/teardown |
| Protocols | [HTTP](protocols/http.md) (requests, responses, cookies, HTTP/2), [WebSocket](protocols/websocket.md), [gRPC](protocols/grpc.md), [GraphQL](protocols/graphql.md), [Kafka](protocols/kafka.md) |
| [Mixed-protocol tests](examples/mixed-protocol.md) | Several protocols in one test, and how it runs |
| [Options](options.md) | Every option, where settings come from, scenarios and executors, thresholds |
| [Command line](cli.md) | Flags, environment variables, output, exit codes, Ctrl+C |
| [Results](results.md) | The summary, what is counted, latency and precision, the time series |
| [JSON summary](json-summary.md) | `--out json` format, schema and versioning |
| [CI](ci/README.md) | GitHub Action, the generic recipe, GitLab CI, Jenkins, Azure Pipelines |
| [Troubleshooting](troubleshooting.md) | Common problems and the messages LoadTool prints |
| [Differences from k6](k6-differences.md) | Porting k6 scripts; what differs and what is missing |
| [Examples](../examples/README.md) | Runnable scripts for every protocol, and the demo API they run against |

**Find a topic:**

| Topic | Where |
|---|---|
| Installation, a first test | [Getting started](getting-started.md) |
| The test script (DSL) | [Script API](script-api.md#a-test-script) |
| HTTP requests and responses | [HTTP](protocols/http.md) |
| Checks | [Script API: `check`](script-api.md#checkvalue-conditions) |
| Thresholds | [Options: Thresholds](options.md#thresholds) |
| Scenarios (constant VUs, ramping, arrival rate) | [Options: Scenarios](options.md#scenarios) |
| Setup and teardown | [Script API: Lifecycle](script-api.md#lifecycle) |
| Cookies and sessions | [HTTP: Cookies and sessions](protocols/http.md#cookies-and-sessions) |
| HTTP/2 | [HTTP: Versions and connections](protocols/http.md#http-versions-and-connections) |
| WebSocket | [WebSocket](protocols/websocket.md) |
| gRPC | [gRPC](protocols/grpc.md) |
| GraphQL | [GraphQL](protocols/graphql.md) |
| Kafka | [Kafka](protocols/kafka.md) |
| Several protocols in one test | [Mixed-protocol tests](examples/mixed-protocol.md) |
| JSON reports | [JSON summary](json-summary.md) |
| HTML reports | [Results: The HTML report](results.md#the-html-report) |
| CI usage | [CI](ci/README.md) |
| Troubleshooting | [Troubleshooting](troubleshooting.md) |

## Working on LoadTool

| Page | What it covers |
|---|---|
| [Contributing](../CONTRIBUTING.md) | Building, testing, the workflow for changes |
| [Architecture](architecture.md) | Components, data flow, Phase 1 build order |
| [The protocol system](architecture/protocol-system.md) | How protocol modules plug into the engine: interfaces, lifecycle, state, metrics, errors (Phase 2) |
| [Workflows](workflows.md) | Diagrams of a test run, development and benchmarking |
| [Decisions](decisions/README.md) | Architecture decision records (ADR-001 onward) |
| [Benchmarks](../benchmarks/README.md) | How LoadTool is measured against k6 and JMeter, and the results |
| [Releasing](releasing.md) | Cutting a release |
