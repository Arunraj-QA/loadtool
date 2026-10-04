# ADR-005: k6-shaped script API

- Status: Accepted
- Date: 2026-10-04

## Context

Phase 1 adds a full test-as-code DSL: modules, checks, thresholds,
scenarios, setup and teardown. Phase 0 exposed only a global `http` object
with `get` and `request`.

The shape of the DSL is the most visible and the hardest to change part of
LoadTool once external users write scripts against it. Three options were
considered:
- **A.** Follow the shape of k6's public API.
- **B.** Design our own API.
- **C.** k6-shaped core, with our own names for LoadTool-specific
  additions.

## Decision

**Option A: follow the shape of k6's public scripting API.** A k6 script
should run on LoadTool after changing its import paths, for the features
Phase 1 implements.

| Area | Shape |
|---|---|
| Modules | `import http from "loadtool/http"`, `import { check, sleep, group } from "loadtool"`. Only import paths differ from k6. |
| Entry points | `export default function (data)`, `export function setup()`, `export function teardown(data)`, named exports used by `scenarios.<name>.exec` |
| Options | `export const options = { vus, duration, scenarios, thresholds, ... }` with k6's key names |
| Executors | `constant-vus`, `ramping-vus`, `constant-arrival-rate`, with k6's parameter names (`stages`, `rate`, `timeUnit`, `preAllocatedVUs`, `maxVUs`, `startTime`, `gracefulStop`, `exec`) |
| Built-in metric names | `http_reqs`, `http_req_duration`, `http_req_failed`, `iterations`, `iteration_duration`, `checks`, `vus`, `vus_max`, `dropped_iterations`, `data_sent`, `data_received` |
| Threshold expressions | `p(95)<500`, `rate<0.01`, `avg<200`, `count>100` on those metric names |
| Response object | `status`, `body`, `json()`, `headers`, `timings.duration`, `error` |
| Globals | `__ENV`, `__VU`, `__ITER` |

### Boundaries

- **Shape only, independent implementation.** k6 is licensed under
  AGPL-3.0, LoadTool under Apache-2.0.
  - LoadTool's implementation is written independently from k6's public
    documentation of behaviour.
  - **No k6 source code, tests or documentation text are copied.**
- **No `k6/...` import aliases.** Scripts import `loadtool/...`. k6 is a
  Grafana Labs trademark, and aliasing its module names would suggest an
  affiliation. Migration means changing import paths, and the docs will
  explain how.
- **Compatibility is a goal, not a guarantee.** Differences are documented
  in a compatibility page as features land. Known ones:
  - No `async`/`await`. goja has no event loop, and LoadTool's HTTP API is
    synchronous.
  - Percentiles are within ±0.78 % (ADR-004), where k6 reports exact
    values.
  - k6 extensions, browser testing, WebSockets, gRPC and other Phase 2+
    features are absent.
- **LoadTool-specific additions** use names that cannot collide with
  k6's. They are documented as LoadTool-only.

## Consequences

- **Lower adoption cost:** users and examples familiar with k6 transfer
  directly, and the threshold and metric vocabulary is already known.
- **Constraints on later ADRs:** the options model, executors, metric names
  and thresholds are fixed to k6's shape. Departures need their own ADR.
- **Behaviour must match the documentation.** Where k6's behaviour is
  documented (for example how executors schedule iterations, or how checks
  count), LoadTool should match it or document the difference. Behaviour is
  verified with LoadTool's own tests, not by porting k6's tests.
- **Breaking changes:** the Phase 0 global `http` object is replaced by the
  `loadtool/http` module. Phase 0 had no external users, so the change is
  made once in Phase 1. The examples are updated at the same time.
