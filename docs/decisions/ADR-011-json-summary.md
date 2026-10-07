# ADR-011: Versioned JSON summary

- Status: Accepted
- Date: 2026-10-06

## Context

Phase 1 roadmap item 9 is JSON output. CI jobs, dashboards and the HTML
report (step 11) need the results in a machine-readable form, and
external users will build on it, so its stability matters more than its
shape.

k6 has two outputs:

- `--summary-export`, an end-of-test JSON summary
- `--out json`, a stream of every data point

Phase 1 needs only the summary: LoadTool keeps fixed-size histograms
(ADR-004), not individual samples.

## Decision

1. **`--summary-json <file>`** writes the end-of-test summary as JSON. It is
   rendered from the same `report.Result` as the console summary, so the
   two always agree.
2. **The format is versioned** with `schemaVersion` (now 1):
   - Adding fields keeps the version; consumers must ignore unknown
     fields.
   - Removing or renaming a field, or changing its meaning or unit, bumps
     it.
   - `docs/json-summary.md` is the reference, and golden files in
     `internal/report/testdata/*.json` pin the output.
3. **Units are fixed and machine-friendly.**
   - Durations are numbers of milliseconds.
   - Fractions are 0–1.
   - Rates per second are over the elapsed time.
   - Values that do not exist are `null`, not 0: latency with no requests
     sent, a check rate with no checks.
   - Metric names are the threshold metric names (ADR-008).
4. **The file is written whenever a result exists**: completed,
   interrupted, failed thresholds or failed teardown.
   - It is written atomically (temporary file, then rename).
   - A run that cannot start writes nothing.
   - A write failure exits 1 after the console summary, taking precedence
     over exit 99.
5. **Streaming output** of individual data points is not part of Phase 1.

## Consequences

- **Schema discipline.** Changing the summary is a schema decision: a test
  fails if the golden JSON changes, and the change must follow the
  versioning rule.
- **Input for the HTML report.** Step 11 renders from the same result
  (or this document).

## Amendment (2026-10-07): `--out json`, outcome and schema

**The flag.**

- `--out json` writes the summary to stdout and moves the console summary
  to stderr, so stdout is valid JSON for pipes.
- `--out json=<file>` writes a file. It is now the documented form;
  `--summary-json <file>` stays as an alias.
- `--out` is repeatable; other values are errors that name the supported
  ones.
- Unlike k6's `--out json`, which streams data points, this is the
  end-of-test summary (decision 5 stands).

**`outcome`** (additive, so schema version 1 is unchanged) holds
`passed`, `exitCode` and `reasons`. They are decided by
`report.Verdict`, the same function the CLI uses for its exit code, so
the document and the process always agree.

**A JSON Schema**, `docs/schemas/summary-v1.schema.json`:

- Its objects are closed, and every always-present field is required.
- Tests validate the golden outputs and a real run (every executor, checks,
  passing and failing thresholds) against it.
- Further tests check that the schema rejects unknown fields, missing
  fields and wrong types, and that every field is required.

**The model stays independent of the format.** `report.Result` stays free
of JSON concerns; the JSON types live only in `internal/report/json.go`.

