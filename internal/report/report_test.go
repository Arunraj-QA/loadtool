package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

// The golden files were captured from the console summary before it moved
// out of internal/cli, so these tests prove the move changed no output.
var goldenCases = map[string]Result{
	"completed": {
		Script: "examples/basic-http.ts", VUs: 100, Duration: 30 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 30*time.Second + 120*time.Millisecond,
		Summary: metrics.Summary{
			Requests: 125430, Successes: 124980, Failures: 450, Sent: 125430, ErrorRate: 450.0 / 125430,
			Min: 9800 * time.Microsecond, Mean: 12345 * time.Microsecond, Max: 1500 * time.Millisecond,
			P50: 11 * time.Millisecond, P90: 15 * time.Millisecond, P95: 18 * time.Millisecond, P99: 250 * time.Millisecond,
			SuccessP50: 11 * time.Millisecond, SuccessP90: 15 * time.Millisecond, SuccessP95: 17 * time.Millisecond, SuccessP99: 40 * time.Millisecond,
			ScriptErrors: 3, FirstScriptError: "Error: boom at default (test.ts:4:9(4))",
		},
	},
	"all-failed": {
		Script: "examples/basic-http.ts", VUs: 100, Duration: 30 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 5 * time.Second,
		Summary: metrics.Summary{
			Requests: 10, Failures: 10, Sent: 10, ErrorRate: 1,
			Min: 300 * time.Microsecond, Mean: 450 * time.Microsecond, Max: 900 * time.Microsecond,
			P50: 400 * time.Microsecond, P90: 800 * time.Microsecond, P95: 850 * time.Microsecond, P99: 900 * time.Microsecond,
		},
	},
	"checks": {
		Script: "examples/checks.ts", VUs: 10, Duration: 30 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 30 * time.Second, Started: time.Date(2026, 10, 6, 9, 30, 0, 123456789, time.FixedZone("IST", 5*3600+1800)),
		Summary: metrics.Summary{
			Requests: 3000, Successes: 3000, Sent: 3000,
			Min: 9 * time.Millisecond, Mean: 11 * time.Millisecond, Max: 30 * time.Millisecond,
			P50: 11 * time.Millisecond, P90: 12 * time.Millisecond, P95: 13 * time.Millisecond, P99: 20 * time.Millisecond,
			SuccessP50: 11 * time.Millisecond, SuccessP90: 12 * time.Millisecond, SuccessP95: 13 * time.Millisecond, SuccessP99: 20 * time.Millisecond,
			Checks: []metrics.CheckResult{
				{Name: "status is 200", Passes: 3000},
				{Name: "has products", Passes: 2994, Fails: 6, FirstError: "SyntaxError: Unexpected token <"},
				{Name: "ünïcode name", Passes: 3000},
			},
		},
	},
	"thresholds": {
		Script: "examples/thresholds.ts", VUs: 10, Duration: 30 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 30 * time.Second,
		Summary: metrics.Summary{
			Requests: 3000, Successes: 2940, Failures: 60, Sent: 3000, ErrorRate: 0.02,
			Protocols: metrics.Protocols{HTTP2: 2900, HTTP1: 100},
			Min:       9 * time.Millisecond, Mean: 11 * time.Millisecond, Max: 30 * time.Millisecond,
			P50: 11 * time.Millisecond, P90: 12 * time.Millisecond, P95: 13 * time.Millisecond, P99: 20 * time.Millisecond,
			SuccessP50: 11 * time.Millisecond, SuccessP90: 12 * time.Millisecond, SuccessP95: 13 * time.Millisecond, SuccessP99: 20 * time.Millisecond,
		},
		Thresholds: []thresholds.Result{
			{Threshold: thresholds.Threshold{Metric: "http_req_duration", Expr: "p(95)<500"}, Observed: 13, Unit: thresholds.Milliseconds, Passed: true},
			{Threshold: thresholds.Threshold{Metric: "http_req_duration", Expr: "p(99)<20.1"}, Observed: 20.05, Unit: thresholds.Milliseconds, Passed: true, Approximate: true},
			{Threshold: thresholds.Threshold{Metric: "http_req_failed", Expr: "rate<0.01"}, Observed: 0.02, Unit: thresholds.Fraction},
			{Threshold: thresholds.Threshold{Metric: "http_reqs", Expr: "rate>50"}, Observed: 100, Unit: thresholds.PerSecond, Passed: true},
			{Threshold: thresholds.Threshold{Metric: "iterations", Expr: "count>1000"}, Observed: 3000, Unit: thresholds.Count, Passed: true},
			{Threshold: thresholds.Threshold{Metric: "checks", Expr: "rate>0.99"}, NoData: true, Unit: thresholds.Fraction},
		},
	},
	"scenarios": {
		Script: "examples/scenarios.ts", VUs: 81, Duration: 70 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 71 * time.Second,
		Scenarios: []config.Scenario{
			{Name: "browse", Executor: config.RampingVUs, Exec: "default", GracefulStop: 30 * time.Second,
				StartVUs: 1, Stages: []config.Stage{{Duration: 30 * time.Second, Target: 50}}, GracefulRampDown: 30 * time.Second},
			{Name: "orders", Executor: config.ConstantArrivalRate, Exec: "placeOrder", StartTime: 10 * time.Second, GracefulStop: 30 * time.Second,
				Rate: 20, TimeUnit: time.Second, Duration: time.Minute, PreAllocatedVUs: 30},
		},
		Series: []metrics.Point{
			{At: time.Second, Requests: 70, Failed: 1, Mean: 11 * time.Millisecond, P50: 11 * time.Millisecond, P95: 13 * time.Millisecond, P99: 20 * time.Millisecond, VUs: 12},
			{At: 2 * time.Second, Requests: 0, VUs: 0},
			{At: 2500 * time.Millisecond, Requests: 35, Mean: 10 * time.Millisecond, P50: 10 * time.Millisecond, P95: 12 * time.Millisecond, P99: 15 * time.Millisecond, VUs: 0},
		},
		Summary: metrics.Summary{
			Requests: 5000, Successes: 5000, Sent: 5000, Iterations: 4900, DroppedIterations: 1234,
			Min: 9 * time.Millisecond, Mean: 11 * time.Millisecond, Max: 30 * time.Millisecond,
			P50: 11 * time.Millisecond, P90: 12 * time.Millisecond, P95: 13 * time.Millisecond, P99: 20 * time.Millisecond,
			SuccessP50: 11 * time.Millisecond, SuccessP90: 12 * time.Millisecond, SuccessP95: 13 * time.Millisecond, SuccessP99: 20 * time.Millisecond,
		},
	},
	// A run mixing HTTP with protocol metric families (ADR-015): used
	// families are reported, the unused grpc_reqs is left out.
	"families": {
		Script: "examples/mixed.ts", VUs: 10, Duration: 10 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 10 * time.Second,
		Summary: metrics.Summary{
			Requests: 1000, Successes: 1000, Sent: 1000, Iterations: 120,
			Min: 9 * time.Millisecond, Mean: 11 * time.Millisecond, Max: 30 * time.Millisecond,
			P50: 11 * time.Millisecond, P90: 12 * time.Millisecond, P95: 13 * time.Millisecond, P99: 20 * time.Millisecond,
			SuccessP50: 11 * time.Millisecond, SuccessP90: 12 * time.Millisecond, SuccessP95: 13 * time.Millisecond, SuccessP99: 20 * time.Millisecond,
			Families: []metrics.FamilySummary{
				{Name: "ws_connecting", Kind: metrics.Trend, Count: 120, Failed: 2,
					Min: 800 * time.Microsecond, Mean: 1500 * time.Microsecond, Max: 9 * time.Millisecond,
					P50: 1200 * time.Microsecond, P90: 2 * time.Millisecond, P95: 3 * time.Millisecond, P99: 8 * time.Millisecond},
				{Name: "ws_msgs_sent", Kind: metrics.Counter, Count: 4000},
				{Name: "ws_session_failed", Kind: metrics.Rate, Count: 120, Trues: 2},
				{Name: "grpc_reqs", Kind: metrics.Counter},
			},
		},
		Thresholds: []thresholds.Result{
			{Threshold: thresholds.Threshold{Metric: "ws_session_failed", Expr: "rate<0.05"}, Observed: 2.0 / 120, Unit: thresholds.Fraction, Passed: true},
		},
	},
	"interrupted-none-sent": {
		Script: "examples/basic-http.ts", VUs: 100, Duration: 30 * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: 2 * time.Second, Interrupted: true,
		Summary: metrics.Summary{
			Requests: 2, Failures: 2, ErrorRate: 1, ScriptErrors: 1, FirstScriptError: "TypeError: x",
		},
	},
}

func TestConsoleMatchesGolden(t *testing.T) {
	for name, r := range goldenCases {
		t.Run(name, func(t *testing.T) {
			var got bytes.Buffer
			Console(&got, r)
			if *update {
				if err := os.WriteFile(filepath.Join("testdata", name+".golden"), got.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != string(want) {
				t.Errorf("output differs from %s.golden\n--- got ---\n%s\n--- want ---\n%s", name, got.String(), want)
			}
		})
	}
}

// The JSON summary of every golden case matches testdata/<name>.json.
// Each document must also be valid JSON with schema version 1.
func TestJSONMatchesGolden(t *testing.T) {
	for name, r := range goldenCases {
		t.Run(name, func(t *testing.T) {
			var got bytes.Buffer
			if err := JSON(&got, r, "v0.0.0-test"); err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(got.Bytes(), &doc); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, got.String())
			}
			if doc["schemaVersion"] != float64(SchemaVersion) {
				t.Errorf("schemaVersion = %v", doc["schemaVersion"])
			}
			if *update {
				if err := os.WriteFile(filepath.Join("testdata", name+".json"), got.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(filepath.Join("testdata", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != string(want) {
				t.Errorf("output differs from %s.json\n--- got ---\n%s\n--- want ---\n%s", name, got.String(), want)
			}
		})
	}
}

func TestFormatCount(t *testing.T) {
	tests := map[int]string{0: "0", 999: "999", 1000: "1,000", 125430: "125,430", 1234567: "1,234,567"}
	for n, want := range tests {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := map[time.Duration]string{
		450 * time.Microsecond:   "450.00µs",
		42500 * time.Microsecond: "42.50ms",
		1500 * time.Millisecond:  "1.50s",
	}
	for d, want := range tests {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// With families, the metrics object is written by its own encoder; text
// in it is still not HTML-escaped, as in the rest of the document.
func TestJSONFamiliesKeepTextUnescaped(t *testing.T) {
	r := goldenCases["families"]
	r.Summary.FirstScriptError = `Error: a < b && c > d`
	var b bytes.Buffer
	if err := JSON(&b, r, "v"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"first": "Error: a < b && c > d"`) {
		t.Errorf("script error was escaped or lost:\n%s", b.String())
	}
}
