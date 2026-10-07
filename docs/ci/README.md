# Running LoadTool in CI

LoadTool is one static binary, so any CI system can run it. A CI job only
needs:

1. the binary, from a GitHub release or built from source with Go;
2. `loadtool run` with the outputs you want to keep;
3. the exit code, which decides whether the job passes.

**Three ways in:**

- **On GitHub:** the [LoadTool Action](#github-actions).
- **Anywhere else:** the [generic recipe](#the-generic-recipe), one shell
  script.
- **On your machine:** the [local reproduction](#reproduce-ci-locally) of
  the CI checks.

None of them needs credentials or an external service.

## Exit codes

| Code | Meaning | Outputs written |
|---|---|---|
| `0` | The test ran and every threshold passed | Yes |
| `99` | The test ran, but at least one threshold failed (as in k6) | Yes |
| `1` | Anything else, in two groups below | Depends on the case |

Exit code 1 covers two kinds of failure:

- **The test could not start:** script error, invalid options, setup
  failure. No result files are written.
- **The test ran but did not finish cleanly:** interrupted, failed
  teardown, or a result file that could not be written. The outputs that
  could be written are kept.

Thresholds are what turn a load test into a pass/fail check. Without
them, a run that completes exits 0 however slow it was. See
[Thresholds](../options.md#thresholds).

## Outputs to keep

| Flag | File | Use |
|---|---|---|
| `--out json=summary.json` | Versioned JSON ([format](../json-summary.md)) | Parse in the pipeline; compare between runs |
| `--report-html report.html` | Self-contained HTML with charts | Attach as an artifact for people to read |

Both are written whenever the test produced a result, including when
thresholds fail, so keep them with "always" or "on failure" artifact
rules.

## GitHub Actions

Use the LoadTool action. In order, it:

1. installs a release, or builds from source;
2. runs the test with the [generic recipe](#the-generic-recipe);
3. writes a job summary with the key numbers and thresholds;
4. uploads the JSON and HTML as an artifact;
5. fails the job on a failed test, with one error annotation per reason
   (such as `threshold failed: http_req_failed rate<0.01`).

**A complete, runnable example:**
[`.github/workflows/load-test-example.yml`](../../.github/workflows/load-test-example.yml).

- It runs on every push to this repository: a deterministic test against
  the bundled target server, with no credentials.
- Its header says what to change when you copy it.

```yaml
jobs:
  load-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: Arunraj-QA/loadtool@v0.1.0   # pin a release
        with:
          script: tests/load/checkout.ts
          args: --vus 20 --duration 2m
        env:
          BASE_URL: https://staging.example.test
```

| Input | Default | Meaning |
|---|---|---|
| `script` | (required) | Test script |
| `args` | `""` | Extra `loadtool run` arguments, split on spaces |
| `version` | `latest` | Release to install, such as `v0.1.0`; `source` builds from the action's checkout with Go (no release needed) |
| `binary` | `""` | Use this binary instead of installing a release |
| `summary-json` | `loadtool-summary.json` | JSON summary path (relative to the workspace) |
| `report-html` | `loadtool-report.html` | HTML report path (relative to the workspace) |
| `upload-artifact` | `true` | Upload both files as an artifact |
| `artifact-name` | `loadtool-report` | Artifact name |

**Outputs:** `exit-code`, `summary-json` and `report-html`.

**Notes:**

- **Environment.** The step's `env` reaches the script's `__ENV`; use it
  for target URLs and credentials (`${{ secrets.… }}`).
- **Soft failures.** To report a failed test without failing the job,
  add `continue-on-error: true` and read `steps.<id>.outputs.exit-code`.
- **Releases.** The action downloads release archives with `gh` and
  checks them against the release's `checksums.txt`. Pin a version so
  runs are repeatable.
- **Before the first release,** or to test an unreleased commit, use
  `version: source` with `uses: Arunraj-QA/loadtool@<commit or branch>`.
  The action sets up the Go version in its `go.mod`, which also becomes
  the job's Go for later steps.
- **What the log shows.** The "Run the test" step prints:
  - the LoadTool version;
  - the console summary;
  - a closing line such as
    `loadtool-ci: FAILED: thresholds failed (exit code 99)`;
  - the files written.

## The generic recipe

[`scripts/loadtool-ci.sh`](../../scripts/loadtool-ci.sh) is the CI logic
in one portable bash script; the Action runs it too. Copy it into your
repository and call it from any CI system:

```bash
LOADTOOL=./loadtool scripts/loadtool-ci.sh tests/load/checkout.ts --vus 20 --duration 2m
```

**It does four things:**

1. Prints the LoadTool version.
2. Removes result files left by an earlier run.
3. Writes `loadtool-summary.json` and `loadtool-report.html`.
4. Ends with a one-line verdict, exiting with LoadTool's exit code: `0`,
   `99` or `1` (see [Exit codes](#exit-codes)).

| Variable | Default | Meaning |
|---|---|---|
| `LOADTOOL` | `loadtool` | The binary |
| `SUMMARY_JSON` | `loadtool-summary.json` | JSON summary path |
| `REPORT_HTML` | `loadtool-report.html` | HTML report path; empty skips it |

Arguments after the script go to `loadtool run`. The script's `__ENV`
comes from the environment (`BASE_URL=…`) or `-e KEY=VALUE`.

**To adapt it to a CI system:**

1. Get the binary: a release ([Any other CI system](#any-other-ci-system)),
   or `go build -o loadtool ./cmd/loadtool` from a checkout.
2. Run the script.
3. Keep both files with an "always" artifact rule, so they are kept when
   the test fails.

## Reproduce CI locally

```bash
scripts/ci-local.sh            # needs Go, bash and curl
```

It builds LoadTool and the demo API, starts the API on port
18090, and runs the deterministic example through the generic recipe
twice:

| Run | Target | Must exit |
|---|---|---|
| passing | the local server | `0` |
| failing | a port nothing listens on, so the error-rate threshold fails | `99` |

For each run it checks the JSON outcome and the HTML report. CI runs the
same script. It takes about 25 seconds and works on Linux, macOS and
Windows (Git Bash).

## Any other CI system

Install a release, run, keep the outputs. On Linux (amd64):

```bash
VERSION=v0.1.0
NAME=loadtool_${VERSION}_linux_amd64
curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/${VERSION}/${NAME}.tar.gz
curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/${VERSION}/checksums.txt
grep " \*\?${NAME}.tar.gz$" checksums.txt | sha256sum -c -
tar -xzf ${NAME}.tar.gz
./${NAME}/loadtool run tests/load/checkout.ts \
  --out json=loadtool-summary.json --report-html loadtool-report.html
```

**Archives.** Release archives are named
`loadtool_<version>_<os>_<arch>.tar.gz` (`.zip` for Windows):

- `os` is `linux`, `darwin` or `windows`.
- `arch` is `amd64` or `arm64`.

Reading results in the pipeline, for example with `jq`:

```bash
jq '.metrics.http_req_duration.p95' loadtool-summary.json
jq -r '.thresholds[] | select(.passed | not) | "\(.metric) \(.expression)"' loadtool-summary.json
```

### GitLab CI

```yaml
load-test:
  image: debian:stable-slim
  variables:
    VERSION: v0.1.0
    BASE_URL: https://staging.example.test
  before_script:
    - apt-get update -qq && apt-get install -qq -y curl ca-certificates
    - NAME=loadtool_${VERSION}_linux_amd64
    - curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/${VERSION}/${NAME}.tar.gz
    - curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/${VERSION}/checksums.txt
    - grep " \*\?${NAME}.tar.gz$" checksums.txt | sha256sum -c -
    - tar -xzf ${NAME}.tar.gz && mv ${NAME}/loadtool /usr/local/bin/
  script:
    - loadtool run tests/load/checkout.ts --out json=loadtool-summary.json --report-html loadtool-report.html
  artifacts:
    when: always
    paths: [loadtool-summary.json, loadtool-report.html]
```

### Jenkins (declarative pipeline)

```groovy
pipeline {
  agent { label 'linux' }
  environment {
    VERSION  = 'v0.1.0'
    BASE_URL = 'https://staging.example.test'
  }
  stages {
    stage('Load test') {
      steps {
        sh '''
          NAME=loadtool_${VERSION}_linux_amd64
          curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/${VERSION}/${NAME}.tar.gz
          curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/${VERSION}/checksums.txt
          grep " \\*\\?${NAME}.tar.gz$" checksums.txt | sha256sum -c -
          tar -xzf ${NAME}.tar.gz
          ./${NAME}/loadtool run tests/load/checkout.ts \
            --out json=loadtool-summary.json --report-html loadtool-report.html
        '''
      }
    }
  }
  post {
    always { archiveArtifacts artifacts: 'loadtool-summary.json, loadtool-report.html', allowEmptyArchive: true }
  }
}
```

### Azure Pipelines

```yaml
steps:
  - bash: |
      NAME=loadtool_$(VERSION)_linux_amd64
      curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/$(VERSION)/${NAME}.tar.gz
      curl -fsSLO https://github.com/Arunraj-QA/loadtool/releases/download/$(VERSION)/checksums.txt
      grep " \*\?${NAME}.tar.gz$" checksums.txt | sha256sum -c -
      tar -xzf ${NAME}.tar.gz
      ./${NAME}/loadtool run tests/load/checkout.ts \
        --out json=loadtool-summary.json --report-html loadtool-report.html
    displayName: Load test
    env:
      BASE_URL: https://staging.example.test
  - publish: loadtool-report.html
    artifact: loadtool-report
    condition: always()
variables:
  VERSION: v0.1.0
```

## Load-testing in CI: practical advice

- **A stable target.** Test a staging environment sized like production.
  A shared CI runner is the load generator; keep the target elsewhere.
- **Short tests, firm thresholds.** A few minutes with
  `http_req_failed: ["rate<0.01"]` and a latency percentile catch
  regressions. Run long soak tests on a schedule, not on every push.
- **Compare like with like.** CI runners vary in CPU, so compare runs of
  the same job; the JSON summary keeps the numbers.
