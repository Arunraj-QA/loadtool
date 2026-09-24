package cli

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/engine"
)

func printSummary(w io.Writer, cfg config.Config, res engine.Result, interrupted bool) {
	s := res.Summary
	status := "completed"
	if interrupted {
		status = "interrupted (partial results)"
	}
	var rps float64
	if secs := res.Elapsed.Seconds(); secs > 0 {
		rps = float64(s.Requests) / secs
	}

	fmt.Fprintf(w, "\nLoadTool summary\n\n")
	fmt.Fprintf(w, "  Target:      GET %s\n", cfg.URL)
	fmt.Fprintf(w, "  VUs:         %d\n", cfg.VUs)
	fmt.Fprintf(w, "  Duration:    %s (elapsed %s)\n", cfg.Duration, formatDuration(res.Elapsed))
	fmt.Fprintf(w, "  Status:      %s\n\n", status)

	fmt.Fprintf(w, "  Requests:    %s (%.1f req/s)\n", formatCount(s.Requests), rps)
	fmt.Fprintf(w, "  Success:     %s\n", formatCount(s.Successes))
	fmt.Fprintf(w, "  Errors:      %s (%.2f%%)\n\n", formatCount(s.Failures), s.ErrorRate*100)

	if s.Requests == 0 {
		fmt.Fprintf(w, "  Latency:     no completed requests\n")
		return
	}
	fmt.Fprintf(w, "  Latency:\n")
	for _, row := range []struct {
		name string
		d    time.Duration
	}{
		{"min", s.Min}, {"mean", s.Mean},
		{"p50", s.P50}, {"p90", s.P90}, {"p95", s.P95}, {"p99", s.P99},
		{"max", s.Max},
	} {
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
