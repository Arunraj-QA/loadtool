package metrics

import (
	"testing"
	"time"
)

func TestMergeEmpty(t *testing.T) {
	if got := Merge(nil); got != (Summary{}) {
		t.Fatalf("Merge(nil) = %+v, want zero Summary", got)
	}
	if got := Merge([]*Recorder{{}, {}}); got != (Summary{}) {
		t.Fatalf("Merge(empty recorders) = %+v, want zero Summary", got)
	}
}

func TestMergeAggregatesAcrossRecorders(t *testing.T) {
	// 1ms..100ms spread over two recorders in reverse order, every 10th
	// request failing, so both merging and sorting are exercised.
	a, b := &Recorder{}, &Recorder{}
	for i := 100; i >= 1; i-- {
		r := a
		if i%2 == 0 {
			r = b
		}
		r.Record(time.Duration(i)*time.Millisecond, i%10 != 0)
	}

	got := Merge([]*Recorder{a, b})
	want := Summary{
		Requests:  100,
		Successes: 90,
		Failures:  10,
		ErrorRate: 0.1,
		Min:       1 * time.Millisecond,
		Mean:      50500 * time.Microsecond,
		Max:       100 * time.Millisecond,
		P50:       50 * time.Millisecond,
		P90:       90 * time.Millisecond,
		P95:       95 * time.Millisecond,
		P99:       99 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("Merge =\n %+v\nwant\n %+v", got, want)
	}
}

func TestPercentile(t *testing.T) {
	ms := func(v ...int) []time.Duration {
		out := make([]time.Duration, len(v))
		for i, x := range v {
			out[i] = time.Duration(x) * time.Millisecond
		}
		return out
	}
	tests := []struct {
		name   string
		sorted []time.Duration
		p      int
		want   time.Duration
	}{
		{"single sample p50", ms(7), 50, 7 * time.Millisecond},
		{"single sample p99", ms(7), 99, 7 * time.Millisecond},
		{"two samples p50", ms(1, 2), 50, 1 * time.Millisecond},
		{"two samples p90", ms(1, 2), 90, 2 * time.Millisecond},
		{"ten samples p95", ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), 95, 10 * time.Millisecond},
		{"ten samples p90", ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), 90, 9 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := percentile(tt.sorted, tt.p); got != tt.want {
				t.Errorf("percentile(p%d) = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}

func TestAllFailures(t *testing.T) {
	r := &Recorder{}
	r.Record(time.Millisecond, false)
	r.Record(time.Millisecond, false)
	s := Merge([]*Recorder{r})
	if s.Requests != 2 || s.Successes != 0 || s.Failures != 2 || s.ErrorRate != 1 {
		t.Fatalf("unexpected summary %+v", s)
	}
}

func TestMergeScriptErrors(t *testing.T) {
	a, b, c := &Recorder{}, &Recorder{}, &Recorder{}
	b.RecordScriptError("first")
	b.RecordScriptError("second")
	c.RecordScriptError("third")
	c.Record(time.Millisecond, true)

	s := Merge([]*Recorder{a, b, c})
	if s.ScriptErrors != 3 || s.FirstScriptError != "first" {
		t.Fatalf("ScriptErrors=%d FirstScriptError=%q, want 3 and %q",
			s.ScriptErrors, s.FirstScriptError, "first")
	}
	if s.Requests != 1 || s.Failures != 0 {
		t.Fatalf("script errors must not change request counts: %+v", s)
	}
}

func TestMergeScriptErrorsWithoutRequests(t *testing.T) {
	r := &Recorder{}
	r.RecordScriptError("boom")
	s := Merge([]*Recorder{r})
	if s.ScriptErrors != 1 || s.FirstScriptError != "boom" || s.Requests != 0 {
		t.Fatalf("unexpected summary %+v", s)
	}
}

func BenchmarkRecorderRecord(b *testing.B) {
	r := &Recorder{}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Record(time.Duration(i), i%100 != 0)
	}
}

func BenchmarkMerge(b *testing.B) {
	// 1,000 VUs x 1,000 requests each.
	recorders := make([]*Recorder, 1000)
	for i := range recorders {
		r := &Recorder{}
		for j := range 1000 {
			r.Record(time.Duration((i*7919+j*104729)%1_000_000), true)
		}
		recorders[i] = r
	}
	b.ReportAllocs()
	for b.Loop() {
		Merge(recorders)
	}
}
