// Package report renders the result of a test run. Every output format
// renders from the same Result, so they always agree.
package report

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

// Result is everything known about a finished run that outputs need.
type Result struct {
	// Script is the path of the test script as given on the command line.
	Script string
	// VUs, Duration and GracefulStop are the run settings.
	VUs          int
	Duration     time.Duration
	GracefulStop time.Duration
	// Elapsed is the wall-clock time from the start of the test clock until
	// the last VU stopped.
	Elapsed time.Duration
	// Interrupted reports whether the run was stopped early (Ctrl+C), so
	// the results are partial.
	Interrupted bool
	Summary     metrics.Summary
	// TeardownError is set when the script's teardown() failed. The load
	// phase's results are still complete.
	TeardownError string
	// Thresholds are the evaluated threshold expressions, in the order
	// the summary lists them.
	Thresholds []thresholds.Result
	// Scenarios describes the scenarios, one line each; empty for the
	// plain vus/duration shorthand.
	Scenarios []string
}

// Console writes the human-readable summary to w.
func Console(w io.Writer, r Result) {
	s := r.Summary
	status := "completed"
	if r.Interrupted {
		status = "interrupted (partial results)"
	}
	var rps float64
	if secs := r.Elapsed.Seconds(); secs > 0 {
		rps = float64(s.Requests) / secs
	}

	fmt.Fprintf(w, "\nLoadTool summary\n\n")
	fmt.Fprintf(w, "  Script:      %s\n", r.Script)
	fmt.Fprintf(w, "  VUs:         %d\n", r.VUs)
	fmt.Fprintf(w, "  Duration:    %s (elapsed %s)\n", r.Duration, formatDuration(r.Elapsed))
	for i, sc := range r.Scenarios {
		label := ""
		if i == 0 {
			label = "Scenarios:"
		}
		fmt.Fprintf(w, "  %-12s %s\n", label, sc)
	}
	fmt.Fprintf(w, "  Status:      %s\n", status)
	if r.TeardownError != "" {
		fmt.Fprintf(w, "  Teardown:    failed (%s)\n", r.TeardownError)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "  Requests:    %s (%.1f req/s)\n", formatCount(s.Requests), rps)
	fmt.Fprintf(w, "  Success:     %s\n", formatCount(s.Successes))
	fmt.Fprintf(w, "  Errors:      %s (%.2f%%)\n", formatCount(s.Failures), s.ErrorRate*100)
	fmt.Fprintf(w, "  Script errs: %s\n", formatCount(s.ScriptErrors))
	if s.FirstScriptError != "" {
		fmt.Fprintf(w, "    first:     %s\n", s.FirstScriptError)
	}
	if s.DroppedIterations > 0 {
		// Arrival-rate starts that found no free VU: the target rate was
		// not reached, so the results describe a lighter load.
		fmt.Fprintf(w, "  Dropped:     %s iterations (no free VU; raise preAllocatedVUs)\n", formatCount(s.DroppedIterations))
	}
	fmt.Fprintln(w)
	printChecks(w, s.Checks)
	printThresholds(w, r.Thresholds)

	if s.Sent == 0 {
		fmt.Fprintf(w, "  Latency:     no requests were sent\n")
		return
	}
	// All requests first: benchmarks/measure.ps1 reads the first p50/p95/p99
	// lines, which keeps LoadTool comparable with k6 and JMeter.
	fmt.Fprintf(w, "  Latency (all requests sent, failed included):\n")
	printLatencies(w, []latencyRow{
		{"min", s.Min}, {"mean", s.Mean},
		{"p50", s.P50}, {"p90", s.P90}, {"p95", s.P95}, {"p99", s.P99},
		{"max", s.Max},
	})
	if s.Successes == 0 {
		fmt.Fprintf(w, "  Latency (successful requests): none succeeded\n")
		return
	}
	fmt.Fprintf(w, "  Latency (successful requests):\n")
	printLatencies(w, []latencyRow{
		{"p50", s.SuccessP50}, {"p90", s.SuccessP90}, {"p95", s.SuccessP95}, {"p99", s.SuccessP99},
	})
}

// printChecks lists each check with its pass rate, ✓ when it never
// failed, and the first error a failing condition threw.
func printChecks(w io.Writer, checks []metrics.CheckResult) {
	if len(checks) == 0 {
		return
	}
	var passes, total, width int
	for _, c := range checks {
		passes += c.Passes
		total += c.Passes + c.Fails
		width = max(width, utf8.RuneCountInString(c.Name))
	}
	fmt.Fprintf(w, "  Checks:      %s / %s passed (%s)\n", formatCount(passes), formatCount(total), percent(passes, total))
	for _, c := range checks {
		mark := "✓"
		if c.Fails > 0 {
			mark = "✗"
		}
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(c.Name))
		n := c.Passes + c.Fails
		fmt.Fprintf(w, "    %s %s%s  %s / %s  %s\n", mark, c.Name, pad, formatCount(c.Passes), formatCount(n), percent(c.Passes, n))
		if c.FirstError != "" {
			fmt.Fprintf(w, "        first error: %s\n", c.FirstError)
		}
	}
	fmt.Fprintln(w)
}

// printThresholds lists each threshold with ✓ or ✗ and the observed value.
// "≈" marks a percentile within the histogram's ±0.78 % of its limit,
// where that error could change the outcome.
func printThresholds(w io.Writer, rs []thresholds.Result) {
	if len(rs) == 0 {
		return
	}
	passed, mw, ew := 0, 0, 0
	for _, r := range rs {
		if r.Passed {
			passed++
		}
		mw = max(mw, len(r.Metric))
		ew = max(ew, utf8.RuneCountInString(r.Expr))
	}
	fmt.Fprintf(w, "  Thresholds:  %d of %d passed\n", passed, len(rs))
	for _, r := range rs {
		mark := "✓"
		if !r.Passed {
			mark = "✗"
		}
		fmt.Fprintf(w, "    %s %-*s  %s%s  %s\n", mark, mw, r.Metric, r.Expr,
			strings.Repeat(" ", ew-utf8.RuneCountInString(r.Expr)), observed(r))
	}
	fmt.Fprintln(w)
}

func observed(r thresholds.Result) string {
	if r.NoData {
		return "no data"
	}
	approx := ""
	if r.Approximate {
		approx = "≈"
	}
	var v string
	switch r.Unit {
	case thresholds.Milliseconds:
		v = fmt.Sprintf("%.2fms", r.Observed)
	case thresholds.Fraction:
		v = fmt.Sprintf("%.4f (%.2f%%)", r.Observed, r.Observed*100)
	case thresholds.PerSecond:
		v = fmt.Sprintf("%.2f/s", r.Observed)
	default:
		v = formatCount(int(r.Observed))
	}
	return "observed " + approx + v
}

// percent formats part/whole with two decimals; 0/0 is shown as "-".
func percent(part, whole int) string {
	if whole == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f%%", float64(part)*100/float64(whole))
}

type latencyRow struct {
	name string
	d    time.Duration
}

func printLatencies(w io.Writer, rows []latencyRow) {
	for _, row := range rows {
		fmt.Fprintf(w, "    %-5s %10s\n", row.name, formatDuration(row.d))
	}
}

// formatDuration prints d with a unit suited to its size and two decimals.
func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.2fµs", float64(d)/float64(time.Microsecond))
	}
}

// formatCount prints a non-negative n with thousands separators, e.g. 125430 -> "125,430".
func formatCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
