package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// sleepIteration records one successful request per iteration and blocks
// until d passes or ctx is done, like a real request would.
func sleepIteration(d time.Duration) IterationFunc {
	return func(ctx context.Context, rec *metrics.Recorder) {
		select {
		case <-time.After(d):
			rec.Record(d, true)
		case <-ctx.Done():
		}
	}
}

func TestRunStopsAfterDuration(t *testing.T) {
	const duration = 100 * time.Millisecond
	res := Run(context.Background(), 4, duration, sleepIteration(5*time.Millisecond))

	if res.Elapsed < duration {
		t.Errorf("Run returned after %v, before duration %v", res.Elapsed, duration)
	}
	// Generous upper bound: iterations are short and stop on ctx.Done.
	if res.Elapsed > duration+time.Second {
		t.Errorf("Run took %v, want close to %v", res.Elapsed, duration)
	}
	if res.Summary.Requests == 0 {
		t.Error("expected some requests to be recorded")
	}
}

func TestRunCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	Run(ctx, 4, time.Hour, sleepIteration(time.Millisecond))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Run did not stop on cancellation, took %v", took)
	}
}

func TestRunAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int64
	res := Run(ctx, 8, time.Hour, func(context.Context, *metrics.Recorder) { calls.Add(1) })
	if calls.Load() != 0 || res.Summary.Requests != 0 {
		t.Fatalf("iterations ran on a cancelled context: calls=%d requests=%d",
			calls.Load(), res.Summary.Requests)
	}
}

func TestRunStartsEveryVUWithOwnRecorder(t *testing.T) {
	const vus = 50
	var mu sync.Mutex
	seen := map[*metrics.Recorder]bool{}
	iter := func(ctx context.Context, rec *metrics.Recorder) {
		mu.Lock()
		seen[rec] = true
		mu.Unlock()
		rec.Record(time.Millisecond, true)
		<-ctx.Done()
	}
	res := Run(context.Background(), vus, 50*time.Millisecond, iter)

	if len(seen) != vus {
		t.Errorf("saw %d distinct recorders, want %d", len(seen), vus)
	}
	if res.Summary.Requests != vus {
		t.Errorf("Requests = %d, want %d", res.Summary.Requests, vus)
	}
}

func TestRunWaitsForAllVUs(t *testing.T) {
	var active atomic.Int64
	iter := func(ctx context.Context, rec *metrics.Recorder) {
		active.Add(1)
		defer active.Add(-1)
		<-ctx.Done()
		// Simulate slow cleanup after cancellation.
		time.Sleep(10 * time.Millisecond)
	}
	Run(context.Background(), 20, 20*time.Millisecond, iter)
	if n := active.Load(); n != 0 {
		t.Fatalf("%d VU iterations still running after Run returned", n)
	}
}
