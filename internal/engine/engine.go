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
	// Elapsed is the wall-clock time from starting the VUs until the last
	// one stopped.
	Elapsed time.Duration
}

// Run initializes vus VUs with newVU, then starts one goroutine per VU that
// calls its iteration in a loop until duration elapses or ctx is cancelled.
// It returns only after every VU goroutine has exited. If any VU fails to
// initialize, no load is generated and the error is returned.
func Run(ctx context.Context, vus int, duration time.Duration, newVU NewVUFunc) (Result, error) {
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

	// The clock starts only after every VU is ready.
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	recorders := make([]*metrics.Recorder, vus)
	var wg sync.WaitGroup
	start := time.Now()
	for i, iter := range iters {
		rec := &metrics.Recorder{}
		recorders[i] = rec
		wg.Go(func() { runVU(ctx, rec, iter) })
	}
	wg.Wait()
	elapsed := time.Since(start)

	return Result{Summary: metrics.Merge(recorders), Elapsed: elapsed}, nil
}

func runVU(ctx context.Context, rec *metrics.Recorder, iter IterationFunc) {
	for ctx.Err() == nil {
		iter(ctx, rec)
	}
}
