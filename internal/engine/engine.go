// Package engine schedules virtual users (VUs) using one goroutine per VU.
//
// The engine is protocol-agnostic: it repeatedly calls an IterationFunc and
// knows nothing about HTTP.
package engine

import (
	"context"
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
	// Started is when the test clock started, after every VU was ready.
	Started time.Time
	// Elapsed is the wall-clock time from the start of the test clock until
	// the last VU stopped.
	Elapsed time.Duration
	// Series is the time series, one point per SampleInterval plus a
	// final, partial one (ADR-012).
	Series []metrics.Point
}

// Run runs one constant-vus scenario: vus VUs, each calling its iteration
// in a loop, for duration (see RunScenarios). If any VU fails to
// initialize, no load is generated and the error is returned.
//
// When duration elapses, VUs stop starting new iterations, but iterations
// already running may finish for up to gracefulStop; only then is their
// context cancelled. Dropping in-flight requests at the deadline would bias
// latency percentiles low, because the requests still running are mostly
// the slow ones. Cancelling ctx (Ctrl+C) stops everything at once.
func Run(ctx context.Context, vus int, duration, gracefulStop time.Duration, newVU NewVUFunc) (Result, error) {
	return RunScenarios(ctx, []Scenario{{
		Name:         "default",
		Executor:     ConstantVUs{VUs: vus, Duration: duration},
		GracefulStop: gracefulStop,
		NewVU:        newVU,
	}})
}
