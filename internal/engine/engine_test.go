package engine

import (
	"context"
	"errors"
	"strings"
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

// shared gives every VU the same stateless iteration.
func shared(iter IterationFunc) NewVUFunc {
	return func(int) (IterationFunc, error) { return iter, nil }
}

// run calls Run and fails the test on an initialization error.
func run(t *testing.T, ctx context.Context, vus int, d time.Duration, iter IterationFunc) Result {
	t.Helper()
	res, err := Run(ctx, vus, d, 0, shared(iter))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestRunStopsAfterDuration(t *testing.T) {
	const duration = 100 * time.Millisecond
	res := run(t, context.Background(), 4, duration, sleepIteration(5*time.Millisecond))

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
	run(t, ctx, 4, time.Hour, sleepIteration(time.Millisecond))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Run did not stop on cancellation, took %v", took)
	}
}

func TestRunAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var inits atomic.Int64
	_, err := Run(ctx, 8, time.Hour, 0, func(int) (IterationFunc, error) {
		inits.Add(1)
		return func(context.Context, *metrics.Recorder) {}, nil
	})
	if err == nil {
		t.Fatal("expected error for a cancelled context")
	}
	if inits.Load() != 0 {
		t.Fatalf("%d VUs were initialized on a cancelled context", inits.Load())
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
	res := run(t, context.Background(), vus, 50*time.Millisecond, iter)

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
	run(t, context.Background(), 20, 20*time.Millisecond, iter)
	if n := active.Load(); n != 0 {
		t.Fatalf("%d VU iterations still running after Run returned", n)
	}
}

func TestRunInitializesEachVUOnce(t *testing.T) {
	const vus = 10
	var ids []int
	iterCalls := make([]atomic.Int64, vus)
	newVU := func(id int) (IterationFunc, error) {
		ids = append(ids, id) // sequential: no lock needed
		return func(ctx context.Context, rec *metrics.Recorder) {
			iterCalls[id].Add(1)
			<-ctx.Done()
		}, nil
	}
	if _, err := Run(context.Background(), vus, 20*time.Millisecond, 0, newVU); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if id != i {
			t.Fatalf("ids = %v, want 0..%d in order", ids, vus-1)
		}
	}
	if len(ids) != vus {
		t.Fatalf("initialized %d VUs, want %d", len(ids), vus)
	}
	for id := range iterCalls {
		if iterCalls[id].Load() != 1 {
			t.Errorf("VU %d iterated %d times, want 1", id, iterCalls[id].Load())
		}
	}
}

func TestRunInitErrorStopsBeforeLoad(t *testing.T) {
	var iterations atomic.Int64
	newVU := func(id int) (IterationFunc, error) {
		if id == 3 {
			return nil, errors.New("bad script")
		}
		return func(context.Context, *metrics.Recorder) { iterations.Add(1) }, nil
	}
	_, err := Run(context.Background(), 5, time.Hour, 0, newVU)
	if err == nil || !strings.Contains(err.Error(), "VU 3") || !strings.Contains(err.Error(), "bad script") {
		t.Fatalf("error = %v, want VU 3 init error", err)
	}
	if n := iterations.Load(); n != 0 {
		t.Fatalf("%d iterations ran despite init failure", n)
	}
}

// slowIteration records one request after d, unless ctx ends first.
func slowIteration(d time.Duration) IterationFunc {
	return func(ctx context.Context, rec *metrics.Recorder) {
		select {
		case <-time.After(d):
			rec.Record(d, true)
		case <-ctx.Done():
		}
	}
}

func TestGracefulStopLetsRunningIterationsFinish(t *testing.T) {
	const vus = 4
	res, err := Run(context.Background(), vus, 50*time.Millisecond, time.Second, shared(slowIteration(200*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	// Each VU's only iteration started at once and outlived the 50 ms
	// duration; the grace period lets it finish and be counted.
	if res.Summary.Requests != vus {
		t.Fatalf("Requests = %d, want %d (iterations running at the deadline must finish)", res.Summary.Requests, vus)
	}
	if res.Elapsed < 200*time.Millisecond || res.Elapsed > time.Second {
		t.Errorf("Elapsed = %v, want about 200ms", res.Elapsed)
	}
}

func TestZeroGracefulStopCancelsAtDeadline(t *testing.T) {
	res, err := Run(context.Background(), 4, 50*time.Millisecond, 0, shared(slowIteration(200*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Requests != 0 {
		t.Fatalf("Requests = %d, want 0 (no grace: in-flight iterations are cancelled)", res.Summary.Requests)
	}
}

func TestGracefulStopIsBounded(t *testing.T) {
	var cancelled atomic.Int64
	iter := func(ctx context.Context, rec *metrics.Recorder) {
		<-ctx.Done() // never finishes on its own
		cancelled.Add(1)
	}
	res, err := Run(context.Background(), 3, 50*time.Millisecond, 100*time.Millisecond, shared(iter))
	if err != nil {
		t.Fatal(err)
	}
	if res.Elapsed < 150*time.Millisecond || res.Elapsed > 2*time.Second {
		t.Errorf("Elapsed = %v, want about 150ms (duration + graceful stop)", res.Elapsed)
	}
	if cancelled.Load() != 3 {
		t.Errorf("%d iterations were cancelled, want 3 once the grace period ended", cancelled.Load())
	}
}

func TestNoIterationStartsAfterDuration(t *testing.T) {
	const duration = 100 * time.Millisecond
	var mu sync.Mutex
	var starts []time.Time
	iter := func(ctx context.Context, rec *metrics.Recorder) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		slowIteration(30*time.Millisecond)(ctx, rec)
	}
	before := time.Now()
	if _, err := Run(context.Background(), 3, duration, time.Second, shared(iter)); err != nil {
		t.Fatal(err)
	}
	// VU start-up here is near-instant, so the engine's clock starts within
	// a few ms of before.
	limit := before.Add(duration + 20*time.Millisecond)
	for _, s := range starts {
		if s.After(limit) {
			t.Fatalf("an iteration started %v after the duration ended", s.Sub(before)-duration)
		}
	}
}

func TestCancelStopsDuringGracefulStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel) // inside the grace period
	iter := func(ctx context.Context, rec *metrics.Recorder) { <-ctx.Done() }

	start := time.Now()
	if _, err := Run(ctx, 3, 50*time.Millisecond, time.Hour, shared(iter)); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Run took %v: cancelling must not wait for the graceful stop", took)
	}
}

// Iterations that finish (here within the grace period) are counted; ones
// cancelled at the deadline are not.
func TestIterationsCountOnlyCompleted(t *testing.T) {
	const vus = 4
	res, err := Run(context.Background(), vus, 50*time.Millisecond, time.Second, shared(slowIteration(200*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Iterations != vus {
		t.Errorf("Iterations = %d, want %d", res.Summary.Iterations, vus)
	}

	res, err = Run(context.Background(), vus, 50*time.Millisecond, 0, shared(slowIteration(200*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Iterations != 0 {
		t.Errorf("Iterations = %d, want 0 when every iteration is cancelled", res.Summary.Iterations)
	}
}
