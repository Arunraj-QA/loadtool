package report

import (
	"reflect"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

func TestVerdict(t *testing.T) {
	pass := thresholds.Result{Threshold: thresholds.Threshold{Metric: "checks", Expr: "rate>0.9"}, Passed: true}
	fail := thresholds.Result{Threshold: thresholds.Threshold{Metric: "http_req_failed", Expr: "rate<0.01"}}
	tests := []struct {
		name string
		r    Result
		want Outcome
	}{
		{"no thresholds", Result{}, Outcome{Passed: true, ExitCode: ExitPassed}},
		{"thresholds pass", Result{Thresholds: []thresholds.Result{pass}}, Outcome{Passed: true, ExitCode: ExitPassed}},
		{"a threshold fails", Result{Thresholds: []thresholds.Result{pass, fail}},
			Outcome{ExitCode: ExitThresholdsFailed, Reasons: []string{"threshold failed: http_req_failed rate<0.01"}}},
		// Partial results are no threshold verdict: exit 1 wins over 99,
		// and every reason is still listed.
		{"interrupted with a failed threshold", Result{Interrupted: true, Thresholds: []thresholds.Result{fail}},
			Outcome{ExitCode: ExitFailed, Reasons: []string{"interrupted", "threshold failed: http_req_failed rate<0.01"}}},
		{"teardown failed", Result{TeardownError: "teardown: Error: boom"},
			Outcome{ExitCode: ExitFailed, Reasons: []string{"teardown: Error: boom"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Verdict(tt.r); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Verdict = %+v, want %+v", got, tt.want)
			}
		})
	}
}
