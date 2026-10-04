package metrics

import (
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

// maxRelErr is the documented percentile precision: half of the widest
// bucket (1/64 of its lower bound).
const maxRelErr = 1.0 / subBuckets / 2

// near reports whether got is within the histogram's precision of want.
func near(got, want time.Duration) bool {
	return math.Abs(float64(got-want)) <= maxRelErr*float64(want)+1
}

func assertNear(t *testing.T, name string, got, want time.Duration) {
	t.Helper()
	if !near(got, want) {
		t.Errorf("%s = %v, want %v ±%.2f%%", name, got, want, maxRelErr*100)
	}
}

func TestMergeEmpty(t *testing.T) {
	if got := Merge(nil); !reflect.DeepEqual(got, Summary{}) {
		t.Fatalf("Merge(nil) = %+v, want zero Summary", got)
	}
	if got := Merge([]*Recorder{{}, {}}); !reflect.DeepEqual(got, Summary{}) {
		t.Fatalf("Merge(empty recorders) = %+v, want zero Summary", got)
	}
	if got := Merge(NewRecorders(4)); !reflect.DeepEqual(got, Summary{}) {
		t.Fatalf("Merge(unused shared recorders) = %+v, want zero Summary", got)
	}
}

func TestMergeAggregatesAcrossRecorders(t *testing.T) {
	// 1ms..100ms over shared-shard recorders in reverse order, every 10th
	// request failing.
	recs := NewRecorders(3)
	for i := 100; i >= 1; i-- {
		recs[i%3].Record(time.Duration(i)*time.Millisecond, i%10 != 0)
	}
	s := Merge(recs)

	// Exact values.
	if s.Requests != 100 || s.Successes != 90 || s.Failures != 10 || s.Sent != 100 || s.ErrorRate != 0.1 {
		t.Fatalf("counts wrong: %+v", s)
	}
	if s.Min != time.Millisecond || s.Max != 100*time.Millisecond || s.Mean != 50500*time.Microsecond {
		t.Fatalf("Min=%v Max=%v Mean=%v, want 1ms, 100ms, 50.5ms exactly", s.Min, s.Max, s.Mean)
	}
	// Percentiles, within the histogram's precision of the exact
	// nearest-rank values.
	assertNear(t, "P50", s.P50, 50*time.Millisecond)
	assertNear(t, "P90", s.P90, 90*time.Millisecond)
	assertNear(t, "P95", s.P95, 95*time.Millisecond)
	assertNear(t, "P99", s.P99, 99*time.Millisecond)
	// Successes are 1..99 ms without multiples of 10 (90 values).
	assertNear(t, "SuccessP50", s.SuccessP50, 49*time.Millisecond) // rank 45
	assertNear(t, "SuccessP90", s.SuccessP90, 89*time.Millisecond) // rank 81
	assertNear(t, "SuccessP95", s.SuccessP95, 95*time.Millisecond) // rank 86
	assertNear(t, "SuccessP99", s.SuccessP99, 99*time.Millisecond) // rank 90
}

// TestBucketBounds checks that every value falls in a bucket whose
// midpoint is within the documented precision, across the tracked range.
func TestBucketBounds(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	check := func(v int64) {
		mid := bucketMid(bucketIndex(v))
		if math.Abs(float64(mid-v)) > maxRelErr*float64(v)+1 {
			t.Fatalf("v=%d: bucket %d midpoint %d is off by more than %.2f%%", v, bucketIndex(v), mid, maxRelErr*100)
		}
	}
	for v := range int64(4096) {
		check(v)
	}
	for range 200_000 {
		// Log-uniform over 1ns..1h.
		check(int64(math.Exp(rng.Float64() * math.Log(float64(maxTracked)))))
	}
	check(maxTracked)
	if got := bucketIndex(maxTracked); got != numBuckets-1 {
		t.Fatalf("maxTracked bucket = %d, want last bucket %d", got, numBuckets-1)
	}
}

func TestBucketIndexIsMonotonic(t *testing.T) {
	prev := 0
	for v := int64(0); v < 1<<20; v++ {
		i := bucketIndex(v)
		if i < prev || i > prev+1 {
			t.Fatalf("bucketIndex(%d) = %d after %d: buckets must be contiguous and increasing", v, i, prev)
		}
		prev = i
	}
}

// TestPercentilesMatchExact compares histogram percentiles with exact
// nearest-rank percentiles on a large, wide, deterministic random sample.
func TestPercentilesMatchExact(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	const n = 200_000
	samples := make([]time.Duration, n)
	r := &Recorder{}
	for i := range samples {
		// Log-uniform 10µs..10s, like latencies spanning several decades.
		d := time.Duration(math.Exp(math.Log(1e4) + rng.Float64()*(math.Log(1e10)-math.Log(1e4))))
		samples[i] = d
		r.Record(d, true)
	}
	slices.Sort(samples)
	exact := func(p int) time.Duration { return samples[max((p*n+99)/100, 1)-1] }

	s := Merge([]*Recorder{r})
	for _, c := range []struct {
		name string
		got  time.Duration
		p    int
	}{{"P50", s.P50, 50}, {"P90", s.P90, 90}, {"P95", s.P95, 95}, {"P99", s.P99, 99}} {
		assertNear(t, c.name, c.got, exact(c.p))
	}
	if s.Min != samples[0] || s.Max != samples[n-1] {
		t.Errorf("Min/Max = %v/%v, want exact %v/%v", s.Min, s.Max, samples[0], samples[n-1])
	}
}

func TestSingleSampleIsExact(t *testing.T) {
	r := &Recorder{}
	r.Record(12345678*time.Nanosecond, true)
	s := Merge([]*Recorder{r})
	// Clamping to the exact min and max makes a single sample exact.
	if s.P50 != 12345678 || s.P99 != 12345678 || s.SuccessP99 != 12345678 {
		t.Fatalf("P50=%v P99=%v SuccessP99=%v, want exactly 12.345678ms", s.P50, s.P99, s.SuccessP99)
	}
}

func TestLatencyAboveTrackedRange(t *testing.T) {
	r := &Recorder{}
	r.Record(3*time.Hour, true)
	s := Merge([]*Recorder{r})
	if s.Max != 3*time.Hour || s.P99 != 3*time.Hour {
		t.Fatalf("Max=%v P99=%v, want 3h (exact max, percentile clamped to it)", s.Max, s.P99)
	}
}

// TestConcurrentRecording exercises shared shards from many goroutines;
// run with -race in CI.
func TestConcurrentRecording(t *testing.T) {
	const vus, perVU = 64, 2000
	recs := NewRecorders(vus)
	var wg sync.WaitGroup
	for i, r := range recs {
		wg.Go(func() {
			for j := range perVU {
				r.Record(time.Duration(1+(i*perVU+j)%1000)*time.Microsecond, j%50 != 0)
			}
		})
	}
	wg.Wait()
	s := Merge(recs)
	if s.Requests != vus*perVU || s.Failures != vus*perVU/50 {
		t.Fatalf("Requests=%d Failures=%d, want %d and %d", s.Requests, s.Failures, vus*perVU, vus*perVU/50)
	}
	if s.Min != time.Microsecond || s.Max != 1000*time.Microsecond {
		t.Fatalf("Min=%v Max=%v, want 1µs and 1ms", s.Min, s.Max)
	}
}

func TestNewRecordersShareAFixedNumberOfShards(t *testing.T) {
	recs := NewRecorders(1000)
	shards := map[*shard]bool{}
	for _, r := range recs {
		shards[r.shard] = true
	}
	if len(shards) != numShards {
		t.Fatalf("1,000 recorders use %d shards, want %d", len(shards), numShards)
	}
	if few := NewRecorders(3); few[0].shard == few[1].shard {
		t.Fatal("fewer recorders than shards should not share a shard")
	}
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
	for range 40 {
		r.Record(50*time.Millisecond, true)
	}
	for range 60 {
		r.Record(100*time.Microsecond, false) // connection refused
	}
	s := Merge([]*Recorder{r})
	assertNear(t, "P50 (distorted by fast failures)", s.P50, 100*time.Microsecond)
	assertNear(t, "SuccessP50", s.SuccessP50, 50*time.Millisecond)
	if s.Min != 100*time.Microsecond {
		t.Errorf("Min = %v, want the fast failures included (100µs)", s.Min)
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
	if s.SuccessP50 != 0 {
		t.Errorf("SuccessP50 = %v, want 0 when nothing succeeded", s.SuccessP50)
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

// BenchmarkRecorderRecord must report 0 B/op: recording does not grow
// memory, however many requests a test makes.
func BenchmarkRecorderRecord(b *testing.B) {
	r := NewRecorders(1)[0]
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Record(time.Duration(i%10_000_000), i%100 != 0)
	}
}

// BenchmarkRecorderRecordParallel records from all CPUs into shared shards.
func BenchmarkRecorderRecordParallel(b *testing.B) {
	recs := NewRecorders(1000)
	var next sync.Mutex
	i := 0
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		next.Lock()
		r := recs[i%len(recs)]
		i++
		next.Unlock()
		for pb.Next() {
			r.Record(12*time.Millisecond, true)
		}
	})
}

func BenchmarkMerge(b *testing.B) {
	// 1,000 VUs x 1,000 requests each.
	recorders := NewRecorders(1000)
	for i, r := range recorders {
		for j := range 1000 {
			r.Record(time.Duration((i*7919+j*104729)%1_000_000), true)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		Merge(recorders)
	}
}
