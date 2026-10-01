package metrics

import (
	"math/rand/v2"
	"slices"
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
		Sent:      100,
		ErrorRate: 0.1,
		Min:       1 * time.Millisecond,
		Mean:      50500 * time.Microsecond,
		Max:       100 * time.Millisecond,
		P50:       50 * time.Millisecond,
		P90:       90 * time.Millisecond,
		P95:       95 * time.Millisecond,
		P99:       99 * time.Millisecond,
		// Successes are 1..99 ms without the multiples of 10 (90 values).
		SuccessP50: 49 * time.Millisecond, // rank 45
		SuccessP90: 89 * time.Millisecond, // rank 81
		SuccessP95: 95 * time.Millisecond, // rank 86
		SuccessP99: 99 * time.Millisecond, // rank 90
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
			if got := percentile(tt.sorted, nil, tt.p); got != tt.want {
				t.Errorf("percentile(p%d) = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}

// TestKthMatchesSortedUnion checks the two-slice selection against sorting
// the concatenation, over many deterministic random cases including empty
// slices and duplicates.
func TestKthMatchesSortedUnion(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for c := 0; c < 2000; c++ {
		a := randomSorted(rng, rng.IntN(12))
		b := randomSorted(rng, rng.IntN(12))
		union := slices.Sorted(slices.Values(append(slices.Clone(a), b...)))
		for k := range union {
			if got := kth(a, b, k); got != union[k] {
				t.Fatalf("kth(%v, %v, %d) = %v, want %v", a, b, k, got, union[k])
			}
		}
	}
}

func randomSorted(rng *rand.Rand, n int) []time.Duration {
	s := make([]time.Duration, n)
	for i := range s {
		s[i] = time.Duration(rng.IntN(20)) // small range: many duplicates
	}
	slices.Sort(s)
	return s
}

func TestRecordUnsentAddsNoLatency(t *testing.T) {
	r := &Recorder{}
	r.Record(10*time.Millisecond, true)
	r.RecordUnsent()
	r.RecordUnsent()
	s := Merge([]*Recorder{r})
	if s.Requests != 3 || s.Failures != 2 || s.Sent != 1 {
		t.Fatalf("Requests=%d Failures=%d Sent=%d, want 3, 2, 1", s.Requests, s.Failures, s.Sent)
	}
	if s.Min != 10*time.Millisecond || s.P50 != 10*time.Millisecond {
		t.Fatalf("unsent requests changed latency: Min=%v P50=%v, want 10ms", s.Min, s.P50)
	}
}

func TestOnlyUnsentRequests(t *testing.T) {
	r := &Recorder{}
	r.RecordUnsent()
	s := Merge([]*Recorder{r})
	if s.Requests != 1 || s.Failures != 1 || s.Sent != 0 || s.ErrorRate != 1 || s.Max != 0 {
		t.Fatalf("unexpected summary %+v", s)
	}
}

// TestSuccessPercentilesExcludeFastFailures reproduces the benchmark case:
// refused connections fail in microseconds and pull all-request
// percentiles down, while success-only percentiles stay true.
func TestSuccessPercentilesExcludeFastFailures(t *testing.T) {
	r := &Recorder{}
	for range 60 {
		r.Record(50*time.Millisecond, true)
	}
	for range 40 {
		r.Record(100*time.Microsecond, false) // connection refused
	}
	s := Merge([]*Recorder{r})
	if s.P50 != 50*time.Millisecond || s.SuccessP50 != 50*time.Millisecond {
		t.Fatalf("P50=%v SuccessP50=%v, want 50ms both", s.P50, s.SuccessP50)
	}
	if s.Min != 100*time.Microsecond {
		t.Errorf("Min = %v, want the fast failures included (100µs)", s.Min)
	}
	r2 := &Recorder{}
	for range 40 {
		r2.Record(50*time.Millisecond, true)
	}
	for range 60 {
		r2.Record(100*time.Microsecond, false)
	}
	s2 := Merge([]*Recorder{r2})
	if s2.P50 != 100*time.Microsecond || s2.SuccessP50 != 50*time.Millisecond {
		t.Fatalf("P50=%v SuccessP50=%v, want 100µs (distorted) and 50ms (true)", s2.P50, s2.SuccessP50)
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
