package thresholds

import (
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

var testFamilies = []metrics.Def{
	{Name: "grpc_req_duration", Kind: metrics.Trend},
	{Name: "grpc_reqs", Kind: metrics.Counter},
	{Name: "grpc_req_failed", Kind: metrics.Rate},
}

// familySummary records into real families, so Evaluate reads the same
// FamilySummary values the runner produces.
func familySummary(t *testing.T) metrics.Summary {
	t.Helper()
	f, err := metrics.NewFamilies(testFamilies)
	if err != nil {
		t.Fatal(err)
	}
	r := metrics.NewRecorders(1)[0]
	r.UseFamilies(f)
	for i := 1; i <= 100; i++ {
		ok := i > 3 // 3 of 100 fail
		r.Trend(0, time.Duration(i)*time.Millisecond, ok)
		r.Add(1, 1)
		r.Rate(2, !ok) // grpc_req_failed: true when the request failed
	}
	return metrics.Summary{Families: f.Summarize()}
}

func TestFamilyThresholds(t *testing.T) {
	ts, err := Parse(map[string][]string{
		"grpc_req_duration": {"p(95)<200", "avg<10", "max<=100", "med<60", "min>=1"},
		"grpc_reqs":         {"count==100", "rate>40"},
		"grpc_req_failed":   {"rate<0.01"},
	}, testFamilies...)
	if err != nil {
		t.Fatal(err)
	}
	rs := Evaluate(ts, familySummary(t), 2*time.Second)
	want := map[string]struct {
		passed bool
		unit   Unit
	}{
		"grpc_req_duration p(95)<200": {true, Milliseconds},
		"grpc_req_duration avg<10":    {false, Milliseconds}, // avg is 50.5 ms
		"grpc_req_duration max<=100":  {true, Milliseconds},
		"grpc_req_duration med<60":    {true, Milliseconds},
		"grpc_req_duration min>=1":    {true, Milliseconds},
		"grpc_reqs count==100":        {true, Count},
		"grpc_reqs rate>40":           {true, PerSecond}, // 50 per second
		"grpc_req_failed rate<0.01":   {false, Fraction}, // 0.03
	}
	if len(rs) != len(want) {
		t.Fatalf("got %d results, want %d", len(rs), len(want))
	}
	for _, r := range rs {
		key := r.Metric + " " + r.Expr
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected threshold %s", key)
			continue
		}
		if r.Passed != w.passed || r.Unit != w.unit || r.NoData {
			t.Errorf("%s: passed %v unit %v noData %v (observed %v); want passed %v unit %v", key, r.Passed, r.Unit, r.NoData, r.Observed, w.passed, w.unit)
		}
	}
}

// A family without samples has no data, and its threshold fails, as for
// built-in metrics; a counter's zero is data.
func TestFamilyThresholdsNoData(t *testing.T) {
	ts, err := Parse(map[string][]string{
		"grpc_req_duration": {"p(95)<200"},
		"grpc_req_failed":   {"rate<0.01"},
		"grpc_reqs":         {"count==0"},
	}, testFamilies...)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := metrics.NewFamilies(testFamilies)
	rs := Evaluate(ts, metrics.Summary{Families: f.Summarize()}, time.Second)
	for _, r := range rs {
		wantNoData := r.Metric != "grpc_reqs"
		if r.NoData != wantNoData || r.Passed == wantNoData {
			t.Errorf("%s %s: noData %v passed %v", r.Metric, r.Expr, r.NoData, r.Passed)
		}
	}
}

func TestFamilyThresholdErrors(t *testing.T) {
	tests := []struct {
		defs map[string][]string
		want string
	}{
		{map[string][]string{"grpc_reqs": {"p(95)<1"}}, "grpc_reqs does not support p(N); use count, rate"},
		{map[string][]string{"grpc_req_failed": {"avg<1"}}, "grpc_req_failed does not support avg; use rate"},
		{map[string][]string{"grpc_req_duration": {"count<1"}}, "does not support count; use avg, min, max, med, p(N)"},
		{map[string][]string{"ws_sessions": {"count>0"}}, "unknown metric \"ws_sessions\"; supported metrics are checks, dropped_iterations, http_req_duration, http_req_failed, http_reqs, iterations, grpc_req_duration, grpc_req_failed, grpc_reqs"},
	}
	for _, tt := range tests {
		if _, err := Parse(tt.defs, testFamilies...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%v) = %v, want an error containing %q", tt.defs, err, tt.want)
		}
	}
	// Without families, a family name is unknown, as in Phase 1.
	if _, err := Parse(map[string][]string{"grpc_reqs": {"count>0"}}); err == nil {
		t.Error("a family threshold parsed without the family declared")
	}
}
