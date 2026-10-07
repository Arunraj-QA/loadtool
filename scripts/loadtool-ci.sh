#!/usr/bin/env bash
# loadtool-ci.sh: the generic CI recipe, usable from any CI system (the
# GitHub Action runs it too).
#
# It runs one LoadTool test and writes the JSON summary and the HTML
# report. It ends with a one-line verdict and exits with loadtool's own
# exit code:
#
#   0   passed
#   99  the test ran, but at least one threshold failed
#   1   anything else: the test could not start, was interrupted, or a
#       teardown or a result file failed
#
# usage: loadtool-ci.sh <script> [loadtool run arguments...]
#
# Environment:
#   LOADTOOL      the binary (default: loadtool on PATH)
#   SUMMARY_JSON  JSON summary path (default: loadtool-summary.json)
#   REPORT_HTML   HTML report path (default: loadtool-report.html; set it
#                 to an empty value to skip the report)
#
# Everything the script's __ENV needs, such as BASE_URL, is passed through
# the environment or as -e KEY=VALUE arguments.
set -uo pipefail

if [ $# -lt 1 ]; then
  echo "usage: loadtool-ci.sh <script> [loadtool run arguments...]" >&2
  exit 1
fi
script=$1
shift
loadtool=${LOADTOOL:-loadtool}
summary=${SUMMARY_JSON:-loadtool-summary.json}
report=${REPORT_HTML-loadtool-report.html}

if ! version=$("$loadtool" --version); then
  echo "loadtool-ci: cannot run '$loadtool'; set LOADTOOL to the binary" >&2
  exit 1
fi
echo "loadtool-ci: $version, running $script"

# A file from an earlier run must not pass for this run's result.
rm -f "$summary"
args=(run "$script" --out "json=$summary")
if [ -n "$report" ]; then
  rm -f "$report"
  args+=(--report-html "$report")
fi

"$loadtool" "${args[@]}" "$@"
code=$?

case $code in
  0) verdict="PASSED" ;;
  99) verdict="FAILED: thresholds failed" ;;
  *) verdict="FAILED: see the errors above" ;;
esac
echo "loadtool-ci: $verdict (exit code $code)"
for f in "$summary" ${report:+"$report"}; do
  if [ -f "$f" ]; then
    echo "loadtool-ci: wrote $f"
  fi
done
exit "$code"
