package thresholds

import (
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// summary builds a Summary from real recordings: 100 requests taking
// 1..100 ms (the last 5 failed), 10 iterations and 4 check runs (3 pass).
func summary() metrics.Summary {
	recs := metrics.NewRecorders(1)
	r := recs[0]
	for i := 1; i <= 100; i++ {
		r.Record(time.Duration(i)*time.Millisecond, i <= 95)
	}
	for range 10 {
		r.RecordIteration()
	}
	for i := range 4 {
		r.RecordCheck("ok", i < 3, "")
	}
	return metrics.Merge(recs)
}

func evaluate(t *testing.T, defs map[string][]string, s metrics.Summary) []Result {
	t.Helper()
	ts, err := Parse(defs)
	if err != nil {
		t.Fatal(err)
	}
	return Evaluate(ts, s, 10*time.Second)
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		metric, expr string
		want         bool
		observed     float64
	}{
		{"http_req_duration", "p(95)<100", true, 95},
		{"http_req_duration", "p(95) < 90", false, 95},
		{"http_req_duration", "p(99.5)<=100", true, 100},
		{"http_req_duration", "med<51", true, 50},
		{"http_req_duration", "avg<50", false, 50.5},
		{"http_req_duration", "min>=1", true, 1},
		{"http_req_duration", "max<100", false, 100},
		{"http_req_failed", "rate<0.1", true, 0.05},
		{"http_req_failed", "rate<0.01", false, 0.05},
		{"http_reqs", "count==100", true, 100},
		{"http_reqs", "rate>=10", true, 10},
		{"iterations", "count>10", false, 10},
		{"iterations", "rate>0.5", true, 1},
		{"checks", "rate>0.7", true, 0.75},
		{"checks", "rate>0.99", false, 0.75},
		{"dropped_iterations", "count==0", true, 0},
	}
	s := summary()
	for _, tt := range tests {
		t.Run(tt.metric+" "+tt.expr, func(t *testing.T) {
			r := evaluate(t, map[string][]string{tt.metric: {tt.expr}}, s)[0]
			if r.NoData {
				t.Fatal("NoData = true")
			}
			if r.Passed != tt.want {
				t.Errorf("Passed = %v, want %v (observed %v)", r.Passed, tt.want, r.Observed)
			}
			// Percentiles come from the histogram: within ±0.78 %.
			if d := r.Observed - tt.observed; d > 0.0078*tt.observed || -d > 0.0078*tt.observed {
				t.Errorf("Observed = %v, want %v", r.Observed, tt.observed)
			}
		})
	}
}

// A threshold that cannot be checked fails rather than passing silently;
// a zero count is data.
func TestNoDataFails(t *testing.T) {
	rs := evaluate(t, map[string][]string{
		"http_req_duration": {"p(95)<500"},
		"http_req_failed":   {"rate<0.01"},
		"checks":            {"rate>0.9"},
		"http_reqs":         {"count==0"},
	}, metrics.Merge(metrics.NewRecorders(1)))
	for _, r := range rs {
		wantNoData := r.Metric != "http_reqs"
		if r.NoData != wantNoData || r.Passed == wantNoData {
			t.Errorf("%s %s: NoData=%v Passed=%v, want NoData=%v", r.Metric, r.Expr, r.NoData, r.Passed, wantNoData)
		}
	}
	if !Failed(rs) {
		t.Error("Failed = false, want true")
	}
}

func TestApproximateNearTheLimit(t *testing.T) {
	s := summary()
	near := evaluate(t, map[string][]string{"http_req_duration": {"p(95)<95.3"}}, s)[0]
	far := evaluate(t, map[string][]string{"http_req_duration": {"p(95)<200"}}, s)[0]
	exact := evaluate(t, map[string][]string{"http_req_duration": {"avg<50.6"}}, s)[0]
	if !near.Approximate || far.Approximate || exact.Approximate {
		t.Errorf("Approximate: near=%v far=%v avg=%v, want true, false, false", near.Approximate, far.Approximate, exact.Approximate)
	}
}

func TestParseOrder(t *testing.T) {
	ts, err := Parse(map[string][]string{
		"http_reqs":         {"count>1"},
		"http_req_duration": {"p(99)<900", "p(95)<500"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, th := range ts {
		got = append(got, th.Metric+" "+th.Expr)
	}
	want := "http_req_duration p(99)<900,http_req_duration p(95)<500,http_reqs count>1"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %q, want %q (by metric, then as written)", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		defs map[string][]string
		want string
	}{
		{map[string][]string{"http_req_durations": {"p(95)<1"}}, `unknown metric "http_req_durations"; supported metrics are checks, dropped_iterations, http_req_duration`},
		{map[string][]string{"http_req_duration{status:200}": {"p(95)<1"}}, "sub-metrics (tags) are not supported yet"},
		{map[string][]string{"http_req_duration": {"p95<1"}}, `"p95<1": expected "<aggregate> <op> <number>"`},
		{map[string][]string{"http_req_duration": {"p(95)<1s"}}, "expected"},
		{map[string][]string{"http_req_duration": {"p(0)<1"}}, "percentile must be above 0"},
		{map[string][]string{"http_req_duration": {"p(101)<1"}}, "at most 100"},
		{map[string][]string{"http_req_duration": {"rate<1"}}, "http_req_duration does not support rate; use avg, min, max, med, p(N)"},
		{map[string][]string{"checks": {"p(95)<1"}}, "checks does not support p(N); use rate"},
		{map[string][]string{"http_reqs": {"avg<1"}}, "use count, rate"},
	}
	for _, tt := range tests {
		_, err := Parse(tt.defs)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%v) error = %v, want it to contain %q", tt.defs, err, tt.want)
		}
	}

	// Every problem is reported at once.
	_, err := Parse(map[string][]string{"nope": {"x"}, "checks": {"rate>1", "bad"}})
	if err == nil || strings.Count(err.Error(), "\n") != 1 {
		t.Errorf("error = %v, want two problems on two lines", err)
	}
}
