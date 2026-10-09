#!/usr/bin/env bash
# ci-local.sh: reproduce the CI load-test checks on your machine.
#
# It builds loadtool and the demo API (examples/server), starts the API,
# and runs the deterministic example through loadtool-ci.sh, the
# same script the GitHub Action uses:
#
#   1. examples/thresholds.ts against the demo API: must exit 0
#   2. the same against a port nothing listens on: must exit 99
#      (every request fails, so the error-rate threshold fails)
#
# For each run it checks the result files and their outcome. It needs Go,
# bash and curl, but no network, credentials or jq.
#
# usage: scripts/ci-local.sh [output dir]   (default: a new temp directory)
#
# Environment:
#   PORT  port for the demo API (default 18090, so it does not clash with
#         one already on 8090)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:-$(mktemp -d)}
port=${PORT:-18090}
mkdir -p "$out"
exe=""
if [ "$(go env GOOS)" = windows ]; then exe=.exe; fi

echo "== build"
(cd "$root" && go build -o "$out/loadtool$exe" ./cmd/loadtool && go build -o "$out/server$exe" ./examples/server)

echo "== start the demo API on 127.0.0.1:$port"
"$out/server$exe" -addr "127.0.0.1:$port" -grpc-addr "" >"$out/server.log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT
for _ in $(seq 50); do
  if curl -sf "http://127.0.0.1:$port/health" >/dev/null; then break; fi
  sleep 0.1
done
curl -sf "http://127.0.0.1:$port/health" >/dev/null || { echo "the server did not start; see $out/server.log" >&2; exit 1; }

failed=0
# run <name> <want exit code> <base URL> <want "exitCode" in the JSON>
run() {
  local name=$1 want=$2 url=$3 code=0
  echo "== $name (want exit code $want)"
  LOADTOOL="$out/loadtool$exe" SUMMARY_JSON="$out/$name-summary.json" REPORT_HTML="$out/$name-report.html" \
    BASE_URL=$url bash "$root/scripts/loadtool-ci.sh" "$root/examples/thresholds.ts" --vus 5 --duration 3s || code=$?
  if [ "$code" != "$want" ]; then
    echo "FAIL $name: exit code $code, want $want" >&2
    failed=1
    return
  fi
  if ! grep -q "\"exitCode\": $want" "$out/$name-summary.json"; then
    echo "FAIL $name: the JSON outcome does not say exit code $want" >&2
    failed=1
  fi
  if ! grep -q "<!doctype html>" "$out/$name-report.html"; then
    echo "FAIL $name: no HTML report" >&2
    failed=1
  fi
}

run passing 0 "http://127.0.0.1:$port"
run failing 99 "http://127.0.0.1:1"

if [ "$failed" != 0 ]; then
  echo "== FAILED; files in $out" >&2
  exit 1
fi
echo "== ok; files in $out"
