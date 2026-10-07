package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

var update = flag.Bool("update", false, "rewrite the HTML golden files and the example report")

// exampleReportPath is the example report users can open from the docs.
// It is rendered from exampleResult, sample data shaped like a real run,
// so it always matches the current template.
var exampleReportPath = filepath.Join("..", "..", "examples", "reports", "example-report.html")

// exampleResult is a 60-second ramping run with a slowdown and an error
// burst from 35 to 42 seconds, so that every chart has something to show
// and the error-rate threshold fails. The numbers are made up, not
// measured; thresholds are judged from them.
func exampleResult() Result {
	const secs = 60
	r := Result{
		Script: "examples/thresholds.ts", VUs: 50, Duration: secs * time.Second, GracefulStop: 30 * time.Second,
		Elapsed: secs*time.Second + 40*time.Millisecond,
		Started: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		Scenarios: []config.Scenario{{Name: "default", Executor: config.RampingVUs, Exec: "default", GracefulStop: 30 * time.Second,
			StartVUs: 0, GracefulRampDown: 30 * time.Second,
			Stages: []config.Stage{{Duration: 20 * time.Second, Target: 50}, {Duration: 30 * time.Second, Target: 50}, {Duration: 10 * time.Second, Target: 0}}}},
	}
	var reqs, failed int
	for i := 1; i <= secs; i++ {
		vus := 50
		switch {
		case i <= 20:
			vus = i * 50 / 20
		case i > 50:
			vus = (secs - i) * 50 / 10
		}
		p := metrics.Point{At: time.Duration(i) * time.Second, VUs: vus,
			Requests: vus * 85, Mean: 12 * time.Millisecond,
			P50: 11 * time.Millisecond, P95: 14 * time.Millisecond, P99: 22 * time.Millisecond}
		if i >= 35 && i <= 42 { // the slowdown: fewer, slower requests and errors
			p.Requests = vus * 30
			p.Failed = p.Requests * (10 + 2*(i-35)) / 100
			p.Mean, p.P50, p.P95, p.P99 = 45*time.Millisecond, 38*time.Millisecond, 160*time.Millisecond, 310*time.Millisecond
		}
		reqs += p.Requests
		failed += p.Failed
		r.Series = append(r.Series, p)
	}
	errorRate := float64(failed) / float64(reqs)
	r.Summary = metrics.Summary{
		Requests: reqs, Successes: reqs - failed, Failures: failed, Sent: reqs, ErrorRate: errorRate,
		Iterations: reqs, Protocols: metrics.Protocols{HTTP1: reqs},
		Min: 9 * time.Millisecond, Mean: 14 * time.Millisecond, Max: 612 * time.Millisecond,
		P50: 11 * time.Millisecond, P90: 13 * time.Millisecond, P95: 17 * time.Millisecond, P99: 95 * time.Millisecond,
		SuccessP50: 11 * time.Millisecond, SuccessP90: 13 * time.Millisecond, SuccessP95: 16 * time.Millisecond, SuccessP99: 60 * time.Millisecond,
		Checks: []metrics.CheckResult{
			{Name: "status is 200", Passes: reqs - failed, Fails: failed},
			{Name: "body has products", Passes: reqs},
		},
	}
	checkRate := float64(2*reqs-failed) / float64(2*reqs)
	r.Thresholds = []thresholds.Result{
		{Threshold: thresholds.Threshold{Metric: "http_req_duration", Expr: "p(95)<200"}, Observed: 17, Unit: thresholds.Milliseconds, Passed: 17 < 200},
		{Threshold: thresholds.Threshold{Metric: "http_req_failed", Expr: "rate<0.01"}, Observed: errorRate, Unit: thresholds.Fraction, Passed: errorRate < 0.01},
		{Threshold: thresholds.Threshold{Metric: "checks", Expr: "rate>0.99"}, Observed: checkRate, Unit: thresholds.Fraction, Passed: checkRate > 0.99},
	}
	return r
}

// The committed example report matches the current template; run
// "go test ./internal/report -run ExampleReport -update" after changing it.
func TestExampleReportIsCurrent(t *testing.T) {
	r := exampleResult()
	if v := Verdict(r); v.Passed || v.ExitCode != ExitThresholdsFailed {
		t.Fatalf("the example should fail a threshold, got %+v", v)
	}
	out := renderHTML(t, r)
	out = strings.Replace(out, "v0.0.0-test", "(example)", -1)
	if *update {
		if err := os.MkdirAll(filepath.Dir(exampleReportPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exampleReportPath, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(exampleReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("%s is out of date; regenerate it with -update", exampleReportPath)
	}
}
