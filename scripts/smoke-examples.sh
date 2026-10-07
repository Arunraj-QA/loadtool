#!/usr/bin/env bash
# Runs every example that targets the local benchmark server and fails if
# one exits non-zero, has script errors or fails a check. CI runs it so
# the examples keep working.
#
#   go run ./benchmarks/server &     # the target, on 127.0.0.1:8080
#   scripts/smoke-examples.sh bin/loadtool
#
# examples/sessions.ts and examples/http2.ts need servers the benchmark
# server does not provide (a login, HTTPS) and are only compiled, by
# TestExamplesLoad.
set -euo pipefail

loadtool=${1:?usage: smoke-examples.sh <loadtool binary>}
jq=${JQ:-jq}
root=$(cd "$(dirname "$0")/.." && pwd)
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

# script, then loadtool run arguments
runs=(
  "basic-http.ts|--vus 2 --duration 2s"
  "basic-http.js|--vus 2 --duration 2s"
  "checks.ts|--vus 2 --duration 2s"
  "thresholds.ts|--vus 2 --duration 2s"
  "lifecycle.ts|--vus 2 --duration 2s"
  "data-driven.ts|--vus 2 --duration 2s"
  "scenarios.ts|"
)

# fail reports a failed example; in GitHub Actions also as an
# annotation, which (unlike the job log) is readable without signing in.
fail() {
  echo "FAIL $1"
  if [ "${GITHUB_ACTIONS:-}" = true ]; then
    msg=${2:-$1}
    msg=${msg//'%'/'%25'}; msg=${msg//$'\r'/}; msg=${msg//$'\n'/'%0A'}
    echo "::error title=example failed::${msg}"
  fi
  failed=1
}

failed=0
for entry in "${runs[@]}"; do
  script=${entry%%|*}
  args=${entry#*|}
  summary="$out/${script}.json"
  code=0
  # shellcheck disable=SC2086
  "$loadtool" run "$root/examples/$script" $args --out "json=$summary" >"$out/${script}.log" 2>&1 || code=$?
  if [ "$code" -ne 0 ]; then
    fail "$script: exit code $code" "$script: exit code $code"$'\n'"$(tail -25 "$out/${script}.log")"
    tail -5 "$out/${script}.log"
    continue
  fi
  problem=$("$jq" -r '
    if .metrics.script_errors.count > 0 then "script errors: \(.metrics.script_errors.first)"
    elif .metrics.http_reqs.count == 0 then "no requests"
    elif .metrics.checks.fails > 0 then "failed checks: \([.checks[] | select(.fails > 0) | .name] | join(", "))"
    else "" end' "$summary")
  if [ -n "$problem" ]; then
    fail "$script: $problem"
  else
    echo "ok   $script ($("$jq" -r '.metrics.http_reqs.count' "$summary") requests)"
  fi
done
exit "$failed"
