package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// Scenario is one workload of a test (ADR-008): an executor that decides
// when its VUs start iterations, and when the scenario runs.
type Scenario struct {
	Name     string
	Executor Executor
	// StartTime delays the scenario from the start of the test clock.
	StartTime time.Duration
	// GracefulStop is how long iterations still running when the executor
	// stops starting new ones may take to finish before they are
	// cancelled.
	GracefulStop time.Duration
	// NewVU creates the scenario's VUs, with id counting from 0 within the
	// scenario. Like every NewVUFunc it is called sequentially, before
	// the clock starts.
	NewVU NewVUFunc
}

// Executor decides when a scenario's VUs start iterations. The set is
// closed: ConstantVUs, RampingVUs and ConstantArrivalRate.
type Executor interface {
	// MaxVUs is how many VUs the scenario needs. All are created before
	// the test clock starts, so memory is known up front.
	MaxVUs() int
	// Length is how long after the scenario's start iterations may start.
	Length() time.Duration

	validate() error
	// drive starts the scenario's goroutines in wg. They must all return
	// once s.ctx is done.
	drive(s *scenarioRun, wg *sync.WaitGroup)
}

// scenarioRun is the state of one running scenario.
type scenarioRun struct {
	// ctx ends at stopStarting + GracefulStop, or when the test is
	// cancelled; in-flight iterations are then cancelled.
	ctx          context.Context
	start        time.Time
	stopStarting time.Time
	iters        []IterationFunc
	recs         []*metrics.Recorder
	// dropped counts iteration starts no VU was free for.
	dropped atomic.Int64
}

// RunScenarios creates every scenario's VUs, then starts the test clock
// and runs the scenarios concurrently, each from its StartTime. It returns
// once every VU goroutine (and scheduler) has exited. If any VU fails to
// initialize, no load is generated and the error is returned.
//
// Each VU's iterations count in the merged Summary; Summary.Iterations
// counts only iterations that returned before their context ended.
func RunScenarios(ctx context.Context, scenarios []Scenario) (Result, error) {
	if len(scenarios) == 0 {
		return Result{}, errors.New("no scenarios to run")
	}
	runs := make([]*scenarioRun, len(scenarios))
	total := 0
	for i, sc := range scenarios {
		if err := sc.Executor.validate(); err != nil {
			return Result{}, fmt.Errorf("scenario %q: %w", sc.Name, err)
		}
		iters := make([]IterationFunc, sc.Executor.MaxVUs())
		for j := range iters {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			iter, err := sc.NewVU(j)
			if err != nil {
				return Result{}, fmt.Errorf("initialize VU %d: %w", total+j, err)
			}
			iters[j] = iter
		}
		runs[i] = &scenarioRun{iters: iters}
		total += len(iters)
	}

	recorders := metrics.NewRecorders(total)
	// The clock starts only after every VU is ready.
	start := time.Now()
	var wg sync.WaitGroup
	next := 0
	for i, sc := range scenarios {
		s := runs[i]
		s.start = start.Add(sc.StartTime)
		s.stopStarting = s.start.Add(sc.Executor.Length())
		var cancel context.CancelFunc
		s.ctx, cancel = context.WithDeadline(ctx, s.stopStarting.Add(sc.GracefulStop))
		defer cancel()
		s.recs = recorders[next : next+len(s.iters)]
		next += len(s.iters)
		sc.Executor.drive(s, &wg)
	}
	wg.Wait()
	elapsed := time.Since(start)

	summary := metrics.Merge(recorders)
	for _, s := range runs {
		summary.DroppedIterations += int(s.dropped.Load())
	}
	return Result{Summary: summary, Started: start, Elapsed: elapsed}, nil
}

// waitUntil blocks until t or until ctx is done, and reports whether t was
// reached with ctx still live.
func waitUntil(ctx context.Context, t time.Time) bool {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

// iterate runs one iteration and counts it if it returned before ctx
// ended; one cut short by a deadline or Ctrl+C is not counted.
func iterate(ctx context.Context, rec *metrics.Recorder, iter IterationFunc) {
	iter(ctx, rec)
	if ctx.Err() == nil {
		rec.RecordIteration()
	}
}
