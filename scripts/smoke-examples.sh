#!/usr/bin/env bash
# Runs every example against the demo API and fails if one exits
# non-zero, has script errors, sends no requests, has failed requests or
# fails a check. CI runs it so the examples keep working.
#
#   go run ./examples/server &       # the demo API, on 127.0.0.1:8090
#   scripts/smoke-examples.sh bin/loadtool
#
# Set BASE_URL if the demo API listens elsewhere.
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
  "post-json.ts|--vus 2 --duration 2s"
  "auth-token.ts|--vus 2 --duration 2s"
  "sessions.ts|--vus 2 --duration 2s"
  "data-driven.ts|--vus 2 --duration 2s"
  "http2.ts|--vus 2 --duration 2s"
  "websocket.ts|--vus 2 --duration 2s"
  "websocket-request-reply.ts|--vus 2 --duration 2s"
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
    elif .metrics.http_req_failed.failed > 0 then "failed requests: \(.metrics.http_req_failed.failed)"
    elif .metrics.checks.fails > 0 then "failed checks: \([.checks[] | select(.fails > 0) | .name] | join(", "))"
    else "" end' "$summary")
  if [ -n "$problem" ]; then
    fail "$script: $problem"
  else
    echo "ok   $script ($("$jq" -r '.metrics.http_reqs.count' "$summary") requests)"
  fi
done
exit "$failed"
