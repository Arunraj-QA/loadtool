// Package engine schedules virtual users (VUs) using one goroutine per VU.
//
// The engine is protocol-agnostic: it repeatedly calls an IterationFunc and
// knows nothing about HTTP.
package engine

import (
	"context"
	"sync"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// IterationFunc performs one iteration for a VU and records its requests in
// rec. It must return promptly once ctx is done. rec is owned by the calling
// VU, so the function must not share it with other goroutines.
type IterationFunc func(ctx context.Context, rec *metrics.Recorder)

// Result is the outcome of a run.
type Result struct {
	Summary metrics.Summary
	// Elapsed is the wall-clock time from starting the VUs until the last
	// one stopped.
	Elapsed time.Duration
}

// Run starts vus goroutines that each call iter in a loop until duration
// elapses or ctx is cancelled. It returns only after every VU goroutine has
// exited.
func Run(ctx context.Context, vus int, duration time.Duration, iter IterationFunc) Result {
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	recorders := make([]*metrics.Recorder, vus)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range recorders {
		rec := &metrics.Recorder{}
		recorders[i] = rec
		wg.Go(func() { runVU(ctx, rec, iter) })
	}
	wg.Wait()
	elapsed := time.Since(start)

	return Result{Summary: metrics.Merge(recorders), Elapsed: elapsed}
}

func runVU(ctx context.Context, rec *metrics.Recorder, iter IterationFunc) {
	for ctx.Err() == nil {
		iter(ctx, rec)
	}
}
