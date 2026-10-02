# Workflows

Three workflow diagrams:
1. [Test run](#1-test-run-workflow): what `loadtool run` does, from command
   to exit code.
2. [Development](#2-development-workflow): how changes are made to
   LoadTool.
3. [Benchmark](#3-benchmark-workflow): how the LoadTool, k6 and JMeter
   comparison is run.

For the components behind these steps, see [architecture.md](architecture.md).

The diagrams are drawn from Mermaid sources in [diagrams/](diagrams/).
After changing a source, regenerate its SVG (see
[diagrams/README.md](diagrams/README.md)).

---

## 1. Test run workflow

What happens for `loadtool run test.ts --vus N --duration D --graceful-stop G`.
- Red boxes end the run with **exit code 1 and no summary**: nothing was
  measured.
- Failed requests and script errors during the load phase are **counted,
  not fatal**: the run completes and exits 0.

![Test run workflow](diagrams/test-run-workflow.svg)

Source: [diagrams/test-run-workflow.mmd](diagrams/test-run-workflow.mmd)

A second Ctrl+C at any point terminates the process immediately: the
first one restores the default signal handling.

---

## 2. Development workflow

How a change goes from request to `main`, following `CLAUDE.md` and the
practice used in Phase 0.

![Development workflow](diagrams/development-workflow.svg)

Source: [diagrams/development-workflow.mmd](diagrams/development-workflow.mmd)

The race detector runs in CI only: it needs cgo, and the Windows
development machine has no C compiler.

---

## 3. Benchmark workflow

How the comparison in `benchmarks/` is run with `measure.ps1`. See
[benchmarks/README.md](../benchmarks/README.md) for the methodology and
metric definitions.

![Benchmark workflow](diagrams/benchmark-workflow.svg)

Source: [diagrams/benchmark-workflow.mmd](diagrams/benchmark-workflow.mmd)
