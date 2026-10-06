package metrics

import (
	"sync"
	"testing"
	"time"
)

func TestSamplerIntervals(t *testing.T) {
	recs := NewRecorders(2)
	s := NewSampler(recs)

	// Interval 1: 100 requests of 1..100 ms, the last 10 failed.
	for i := 1; i <= 100; i++ {
		recs[i%2].Record(time.Duration(i)*time.Millisecond, i <= 90)
	}
	p := s.Sample(time.Second, 2)
	if p.At != time.Second || p.Requests != 100 || p.Failed != 10 || p.VUs != 2 {
		t.Fatalf("point 1 = %+v", p)
	}
	near := func(got, want time.Duration) bool {
		d := float64(got - want)
		return d <= 0.0078*float64(want) && -d <= 0.0078*float64(want)
	}
	if !near(p.P50, 50*time.Millisecond) || !near(p.P95, 95*time.Millisecond) || !near(p.P99, 99*time.Millisecond) {
		t.Errorf("percentiles p50=%v p95=%v p99=%v, want about 50, 95, 99ms", p.P50, p.P95, p.P99)
	}
	if p.Mean != 50500*time.Microsecond {
		t.Errorf("mean = %v, want 50.5ms (exact)", p.Mean)
	}

	// Interval 2: nothing happened.
	if p := s.Sample(2*time.Second, 1); p.Requests != 0 || p.Failed != 0 || p.P95 != 0 || p.Mean != 0 || p.VUs != 1 {
		t.Errorf("empty interval = %+v", p)
	}

	// Interval 3: only the new requests count, not the earlier ones.
	for range 5 {
		recs[0].Record(200*time.Millisecond, true)
	}
	if p := s.Sample(3*time.Second, 2); p.Requests != 5 || p.Failed != 0 || !near(p.P50, 200*time.Millisecond) || p.Mean != 200*time.Millisecond {
		t.Errorf("point 3 = %+v, want 5 requests at 200ms", p)
	}
}

// Sampling while VUs record (run with -race) loses no request: the
// intervals add up to the total.
func TestSamplerConcurrentWithRecording(t *testing.T) {
	recs := NewRecorders(4)
	s := NewSampler(recs)
	const perVU = 5000
	var wg sync.WaitGroup
	for _, r := range recs {
		wg.Go(func() {
			for i := range perVU {
				r.Record(time.Duration(i%100)*time.Millisecond, i%10 != 0)
			}
		})
	}
	total, failed := 0, 0
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for i := 1; ; i++ {
		select {
		case <-done:
			p := s.Sample(time.Duration(i)*time.Millisecond, 0)
			total, failed = total+p.Requests, failed+p.Failed
			if total != 4*perVU {
				t.Errorf("intervals add up to %d requests, want %d", total, 4*perVU)
			}
			if failed != 4*perVU/10 {
				t.Errorf("intervals add up to %d failures, want %d", failed, 4*perVU/10)
			}
			return
		default:
			p := s.Sample(time.Duration(i)*time.Millisecond, 0)
			total, failed = total+p.Requests, failed+p.Failed
		}
	}
}

func BenchmarkSample(b *testing.B) {
	recs := NewRecorders(1000) // 16 shards, as in a 1,000-VU run
	s := NewSampler(recs)
	b.ReportAllocs()
	for b.Loop() {
		s.Sample(time.Second, 0)
	}
}
