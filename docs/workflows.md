# Workflows

Three workflow diagrams:
1. [Test run](#1-test-run-workflow): what `loadtool run` does, from command
   to exit code.
2. [Development](#2-development-workflow): how changes are made to
   LoadTool.
3. [Benchmark](#3-benchmark-workflow): how the LoadTool, k6 and JMeter
   comparison is run.

For the components behind these steps, see [architecture.md](architecture.md).

---

## 1. Test run workflow

What happens for `loadtool run test.ts --vus N --duration D --graceful-stop G`.
- Red boxes end the run with **exit code 1 and no summary**: nothing was
  measured.
- Failed requests and script errors during the load phase are **counted,
  not fatal**: the run completes and exits 0.

```mermaid
flowchart TD
    start(["loadtool run test.ts --vus N --duration D --graceful-stop G"]) --> args{"Exactly one script argument?"}
    args -->|no| failArgs["Error: accepts 1 arg"]
    args -->|yes| cfg{"config.Validate:<br/>script set, VUs ≥ 1,<br/>D > 0, G ≥ 0?"}
    cfg -->|no| failCfg["Error: lists every invalid setting"]
    cfg -->|yes| load["script.Load: read file"]
    load --> build{"esbuild bundle:<br/>valid syntax, no imports,<br/>default export present?"}
    build -->|no| failBuild["Error: load script<br/>(file:line:column)"]
    build -->|yes| compile["goja.Compile → shared Program"]
    compile --> client["httpclient.New:<br/>one HTTP/1.1 client,<br/>≤ 1 connection per VU"]

    client --> initLoop["Start VU i (sequentially, before the clock)"]
    initLoop --> newVU["New goja runtime, call depth ≤ 2,500,<br/>run top-level code once"]
    newVU --> initOK{"Top-level code OK and<br/>default export is a function?"}
    initOK -->|"no: throw, HTTP call,<br/>stack overflow"| failInit["Error: initialize VU i"]
    initOK -->|"Ctrl+C during start-up"| failCancel["Error: context canceled"]
    initOK -->|yes| moreVUs{"More VUs to start?"}
    moreVUs -->|yes| initLoop
    moreVUs -->|no| clock["Start the clock:<br/>stop starting at D,<br/>hard stop at D + G"]

    clock --> vuLoop{"Each VU, in its own goroutine:<br/>before D and not cancelled?"}
    vuLoop -->|yes| iterate["Call default()"]
    iterate --> req["http.get / http.request → send, time, read body"]
    req --> outcome{"Outcome"}
    outcome -->|"2xx/3xx"| recOK["Record success + latency"]
    outcome -->|"4xx/5xx, refused,<br/>timeout"| recFail["Record failure + latency"]
    outcome -->|"never sent<br/>(invalid URL)"| recUnsent["Record failure, no latency"]
    iterate -->|"script throws or<br/>stack overflow"| recScript["Count script error<br/>(keep first message)"]
    recOK & recFail & recUnsent & recScript --> vuLoop

    vuLoop -->|"D reached"| grace["Stop starting iterations;<br/>running ones may finish until D + G"]
    grace --> hard{"Finished before D + G?"}
    hard -->|yes| counted["Their requests are counted"]
    hard -->|no| dropped["Cancelled at D + G:<br/>not counted"]
    vuLoop -->|"Ctrl+C"| interrupt["Cancel everything now:<br/>requests aborted, JS interrupted"]

    counted & dropped & interrupt --> wait["Wait for all VU goroutines"]
    wait --> merge["metrics.Merge → Summary"]
    merge --> print["Print summary: requests, errors,<br/>latency (all sent / successful only)"]
    print --> interrupted{"Interrupted by Ctrl+C?"}
    interrupted -->|no| exit0(["Exit 0"])
    interrupted -->|yes| exit1(["Exit 1: partial results"])

    classDef fail fill:#fde2e1,stroke:#c0392b,color:#7b241c
    class failArgs,failCfg,failBuild,failInit,failCancel fail
```

A second Ctrl+C at any point terminates the process immediately: the
first one restores the default signal handling.

---

## 2. Development workflow

How a change goes from request to `main`, following `CLAUDE.md` and the
practice used in Phase 0.

```mermaid
flowchart TD
    req(["Feature or fix request"]) --> scope{"Phase 0 scope?"}
    scope -->|"no: later phase"| ask["Explain which phase it belongs to;<br/>ask whether Phase 0 needs it"]
    scope -->|yes| inspect["Inspect the existing code<br/>(and the Go / library source when behaviour matters)"]
    inspect --> explain["Explain the proposed change briefly"]
    explain --> major{"Significant architecture<br/>decision or trade-off?"}
    major -->|yes| decide["Present the trade-offs, with measurements;<br/>the user decides"]
    decide --> adr["Write an ADR:<br/>docs/decisions/ADR-NNN-name.md"]
    major -->|no| branch
    adr --> branch["Create a feature branch"]
    branch --> impl["Implement the smallest working change"]
    impl --> tests["Add tests; for fixes, show each test<br/>fails without the fix (mutation check)"]
    tests --> local{"gofmt, go vet,<br/>go test ./... pass?"}
    local -->|no| impl
    local -->|yes| perf{"Performance-sensitive?"}
    perf -->|yes| bench["Run benchmarks and compare with the<br/>previous build (alternating A/B);<br/>record results in benchmarks/results/"]
    perf -->|no| review
    bench --> review["Review for goroutine leaks, races,<br/>allocations and resource cleanup"]
    review --> docs["Update README / ADRs / architecture"]
    docs --> commit["Small commits, one concern each;<br/>every commit builds"]
    commit --> push["Push the branch"]
    push --> ci{"CI: gofmt, vet, tests on Linux and Windows,<br/>go test -race -count=3 on Linux"}
    ci -->|fail| impl
    ci -->|pass| approve{"User approves merging?"}
    approve -->|no| hold["Branch stays open"]
    approve -->|yes| ff["Fast-forward main and push"]
    ff --> done(["Done"])
```

The race detector runs in CI only: it needs cgo, and the Windows
development machine has no C compiler.

---

## 3. Benchmark workflow

How the comparison in `benchmarks/` is run with `measure.ps1`. See
[benchmarks/README.md](../benchmarks/README.md) for the methodology and
metric definitions.

```mermaid
flowchart TD
    prep(["Prepare"]) --> build["Build bin/loadtool.exe and bin/benchserver.exe"]
    build --> jmeter["Provide stock JMeter 5.6.3:<br/>no plugins in lib/ext, official properties"]
    jmeter --> run["./benchmarks/measure.ps1 -VUs 100,250,500,750,1000 ..."]
    run --> fresh{"OutDir already has runs.jsonl?"}
    fresh -->|yes| refuse["Stop: choose a new OutDir<br/>(sessions are never mixed)"]
    fresh -->|no| env["Record environment.json:<br/>CPU, RAM, OS, power, tool versions, commit"]
    env --> server["Start benchserver<br/>GET /api/test, fixed body, fixed delay"]
    server --> health{"GET /health OK?"}
    health -->|no| abort["Stop: server not healthy"]
    health -->|yes| level["Next VU level"]

    level --> warm["Warm-up: each tool once, 30 s<br/>(recorded as kind=warmup, excluded later)"]
    warm --> round["Measured round r of 3"]
    round --> tool["Next tool: LoadTool → k6 → JMeter"]
    tool --> launch["Launch the tool<br/>(JMeter: measure its java.exe child)"]
    launch --> sample["Sample the process every 250 ms:<br/>private bytes, working set"]
    sample --> exited["Process exits: read CPU time and wall time"]
    exited --> parse["Read the tool's own results:<br/>LoadTool summary · k6 JSON ·<br/>JMeter summary + JTL percentiles"]
    parse --> calc["Calculate CPU %, average memory,<br/>error rate, server CPU %"]
    calc --> append["Append one line to runs.jsonl;<br/>missing values stay empty"]
    append --> cool["Cool down 5 s"]
    cool --> moreTools{"More tools this round?"}
    moreTools -->|yes| tool
    moreTools -->|no| moreRounds{"More rounds?"}
    moreRounds -->|yes| round
    moreRounds -->|no| moreLevels{"More VU levels?"}
    moreLevels -->|yes| level
    moreLevels -->|no| stop["Stop the server"]

    stop --> summarize["./benchmarks/summarize.ps1:<br/>median (min–max) → summary.md / summary.csv"]
    summarize --> verify["Check the claims against raw data:<br/>error causes, overlap of ranges"]
    verify --> errs{"Target errors in the runs?"}
    errs -->|yes| caveat["Report throughput and latency as not<br/>comparable at those levels (environment-bound)"]
    errs -->|no| report
    caveat --> report["Write the report: environment, configuration,<br/>measured values · calculated values · observations"]
    report --> commitRes(["Commit the report and raw data<br/>(JMeter JTLs kept outside git, SHA-256 recorded)"])
```
