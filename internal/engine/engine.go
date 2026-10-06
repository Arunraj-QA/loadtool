// Package engine schedules virtual users (VUs) using one goroutine per VU.
//
// The engine is protocol-agnostic: it repeatedly calls an IterationFunc and
// knows nothing about HTTP.
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// IterationFunc performs one iteration for a VU and records its requests in
// rec. It must return promptly once ctx is done. rec is owned by the calling
// VU, so the function must not share it with other goroutines.
type IterationFunc func(ctx context.Context, rec *metrics.Recorder)

// NewVUFunc creates the per-VU state and returns the iteration to run. It is
// called once per VU, sequentially, before the test clock starts, so any
// state it creates is owned by exactly one VU.
type NewVUFunc func(id int) (IterationFunc, error)

// Result is the outcome of a run.
type Result struct {
	Summary metrics.Summary
	// Elapsed is the wall-clock time from the start of the test clock until
	// the last VU stopped.
	Elapsed time.Duration
}

// Run initializes vus VUs with newVU, then starts one goroutine per VU that
// calls its iteration in a loop. It returns only after every VU goroutine
// has exited. If any VU fails to initialize, no load is generated and the
// error is returned.
//
// When duration elapses, VUs stop starting new iterations, but iterations
// already running may finish for up to gracefulStop; only then is their
// context cancelled. Dropping in-flight requests at the deadline would bias
// latency percentiles low, because the requests still running are mostly
// the slow ones. Cancelling ctx (Ctrl+C) stops everything at once.
func Run(ctx context.Context, vus int, duration, gracefulStop time.Duration, newVU NewVUFunc) (Result, error) {
	iters := make([]IterationFunc, vus)
	for i := range iters {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		iter, err := newVU(i)
		if err != nil {
			return Result{}, fmt.Errorf("initialize VU %d: %w", i, err)
		}
		iters[i] = iter
	}

	// The clock starts only after every VU is ready. start is taken before
	// the deadlines are set so Elapsed is never shorter than duration.
	start := time.Now()
	stopStarting := start.Add(duration)
	ctx, cancel := context.WithDeadline(ctx, stopStarting.Add(gracefulStop))
	defer cancel()

	recorders := metrics.NewRecorders(vus)
	var wg sync.WaitGroup
	for i, iter := range iters {
		rec := recorders[i]
		wg.Go(func() { runVU(ctx, stopStarting, rec, iter) })
	}
	wg.Wait()
	elapsed := time.Since(start)

	return Result{Summary: metrics.Merge(recorders), Elapsed: elapsed}, nil
}

// runVU starts iterations until stopStarting, and stops early if ctx ends.
// An iteration counts as completed when it returns before ctx ends; one
// cut short by the end of the test or Ctrl+C is not counted.
func runVU(ctx context.Context, stopStarting time.Time, rec *metrics.Recorder, iter IterationFunc) {
	for ctx.Err() == nil && time.Now().Before(stopStarting) {
		iter(ctx, rec)
		if ctx.Err() == nil {
			rec.RecordIteration()
		}
	}
}
