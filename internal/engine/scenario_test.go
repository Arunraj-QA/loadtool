package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// active reports whether VU i is active at offset t, straight from the
// definition: the stage line's value at t is at least i+1.
func (e RampingVUs) active(i int, t time.Duration) bool {
	segStart, from := time.Duration(0), float64(e.StartVUs)
	for _, st := range e.Stages {
		to := float64(st.Target)
		if st.Duration == 0 {
			if t >= segStart {
				from = to
			}
			continue
		}
		if t < segStart+st.Duration {
			frac := float64((t - segStart).Microseconds()) / float64(st.Duration.Microseconds())
			return from+(to-from)*frac >= float64(i+1)-1e-9
		}
		segStart, from = segStart+st.Duration, to
	}
	return from >= float64(i+1)
}

func TestRampingNextExamples(t *testing.T) {
	up := RampingVUs{Stages: []Stage{{10 * time.Second, 10}}}
	down := RampingVUs{StartVUs: 10, Stages: []Stage{{10 * time.Second, 0}}}
	tests := []struct {
		name   string
		e      RampingVUs
		vu     int
		from   time.Duration
		want   bool
		at     time.Duration
		wantOK bool
	}{
		{"first VU joins after 1s", up, 0, 0, true, time.Second, true},
		{"fifth VU joins after 5s", up, 4, 0, true, 5 * time.Second, true},
		{"last VU would join only as the stages end", up, 9, 0, true, 0, false},
		{"top VU leaves at once", down, 9, 0, false, time.Microsecond, true},
		{"first VU leaves last", down, 0, 0, false, 9*time.Second + time.Microsecond, true},
		{"a VU that never leaves", up, 0, 2 * time.Second, false, 0, false},
		{"inactive before joining", up, 0, 0, false, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at, ok := tt.e.next(tt.vu, tt.from, tt.want)
			if ok != tt.wantOK || (ok && at != tt.at) {
				t.Errorf("next = %v, %v; want %v, %v", at, ok, tt.at, tt.wantOK)
			}
		})
	}
}

// For random stages and times, next returns the earliest offset at which
// the VU is in the wanted state, at microsecond resolution.
func TestRampingNextMatchesDefinition(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 300 {
		e := RampingVUs{StartVUs: r.IntN(5)}
		for range 1 + r.IntN(4) {
			e.Stages = append(e.Stages, Stage{time.Duration(r.IntN(4)) * time.Millisecond, r.IntN(6)})
		}
		length := e.Length()
		for range 20 {
			vu, want := r.IntN(6), r.IntN(2) == 0
			from := time.Duration(r.Int64N(int64(length) + 1)).Truncate(time.Microsecond)
			at, ok := e.next(vu, from, want)
			// Scan forward in microseconds for the true answer.
			var truth time.Duration
			found := false
			for x := from; x < length; x += time.Microsecond {
				if e.active(vu, x) == want {
					truth, found = x, true
					break
				}
			}
			if ok != found || (found && at != truth) {
				t.Fatalf("stages %+v start %d: next(vu %d, %v, %v) = %v, %v; want %v, %v",
					e.Stages, e.StartVUs, vu, from, want, at, ok, truth, found)
			}
		}
	}
}

// starts records, per VU, the offsets at which iterations started.
type starts struct {
	mu    sync.Mutex
	begin time.Time
	byVU  map[int][]time.Duration
}

func newStarts() *starts { return &starts{byVU: make(map[int][]time.Duration)} }

// newVU returns VUs whose iterations record their start and then take d.
func (s *starts) newVU(d time.Duration) NewVUFunc {
	return func(id int) (IterationFunc, error) {
		return func(ctx context.Context, rec *metrics.Recorder) {
			s.mu.Lock()
			if s.begin.IsZero() {
				panic("begin not set")
			}
			s.byVU[id] = append(s.byVU[id], time.Since(s.begin))
			s.mu.Unlock()
			select {
			case <-time.After(d):
				rec.Record(d, true)
			case <-ctx.Done():
			}
		}, nil
	}
}

// run runs scenarios with begin set just before the clock starts. Offsets
// are therefore at most a few microseconds early relative to the engine's
// clock, never late, so lower bounds hold.
func (s *starts) run(t *testing.T, scenarios ...Scenario) Result {
	t.Helper()
	s.begin = time.Now()
	res, err := RunScenarios(context.Background(), scenarios)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRampingVUsJoinInOrder(t *testing.T) {
	// 0 -> 4 VUs over 80ms, then 4 VUs for 80ms: VU i joins at (i+1)*20ms.
	e := RampingVUs{Stages: []Stage{{80 * time.Millisecond, 4}, {80 * time.Millisecond, 4}}}
	s := newStarts()
	res := s.run(t, Scenario{Name: "ramp", Executor: e, GracefulStop: time.Second, NewVU: s.newVU(5 * time.Millisecond)})
	for vu := range 4 {
		got := s.byVU[vu]
		if len(got) == 0 {
			t.Fatalf("VU %d never ran", vu)
		}
		if join := time.Duration(vu+1) * 20 * time.Millisecond; got[0] < join-time.Millisecond {
			t.Errorf("VU %d started at %v, before it joins at %v", vu, got[0], join)
		}
		if last := got[len(got)-1]; last >= 160*time.Millisecond+5*time.Millisecond {
			t.Errorf("VU %d started an iteration at %v, after the stages end", vu, last)
		}
	}
	if res.Summary.Iterations == 0 || res.Summary.DroppedIterations != 0 {
		t.Errorf("Summary = %+v", res.Summary)
	}
}

func TestRampingVUsLeaveInOrder(t *testing.T) {
	// Jump to 4 VUs, then 4 -> 0 over 100ms: VU i leaves after (3-i)*25ms.
	e := RampingVUs{Stages: []Stage{{0, 4}, {100 * time.Millisecond, 0}}}
	s := newStarts()
	s.run(t, Scenario{Name: "down", Executor: e, GracefulStop: time.Second, NewVU: s.newVU(2 * time.Millisecond)})
	for vu := range 4 {
		leave := time.Duration(3-vu) * 25 * time.Millisecond
		for _, at := range s.byVU[vu] {
			// The VU checks it is active right before starting, so an
			// iteration can begin only a moment after it leaves.
			if at > leave+10*time.Millisecond {
				t.Errorf("VU %d started an iteration at %v, after leaving at %v", vu, at, leave)
			}
		}
	}
	if len(s.byVU[0]) == 0 {
		t.Error("VU 0, active the longest, never ran")
	}
}

// A VU removed during an iteration may finish it for GracefulRampDown,
// then the iteration is cancelled (and not counted).
func TestGracefulRampDownCancelsLongIterations(t *testing.T) {
	e := RampingVUs{
		StartVUs:         1,
		Stages:           []Stage{{50 * time.Millisecond, 1}, {0, 0}, {200 * time.Millisecond, 0}},
		GracefulRampDown: 30 * time.Millisecond,
	}
	s := newStarts()
	start := time.Now()
	res := s.run(t, Scenario{Name: "rd", Executor: e, GracefulStop: 10 * time.Second, NewVU: s.newVU(10 * time.Second)})
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Run took %v; the iteration was not cancelled at the ramp-down deadline", took)
	}
	if res.Summary.Iterations != 0 || len(s.byVU[0]) != 1 {
		t.Errorf("Iterations = %d, starts = %v; want one cancelled iteration", res.Summary.Iterations, s.byVU[0])
	}
}

// Every scheduled start is either run or dropped: starts are not lost or
// invented, whatever the timing.
func TestArrivalRateStartsAreRunOrDropped(t *testing.T) {
	tests := []struct {
		name     string
		vus      int
		iterTime time.Duration
	}{
		{"enough VUs", 4, time.Millisecond},
		{"one slow VU drops starts", 1, 50 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 100 per second for 200ms: starts at 0, 10ms, ... 190ms.
			e := ConstantArrivalRate{Rate: 100, TimeUnit: time.Second, Duration: 200 * time.Millisecond, PreAllocatedVUs: tt.vus}
			s := newStarts()
			res := s.run(t, Scenario{Name: "rate", Executor: e, GracefulStop: time.Second, NewVU: s.newVU(tt.iterTime)})
			sum := res.Summary
			if got := sum.Iterations + sum.DroppedIterations; got != 20 {
				t.Errorf("iterations %d + dropped %d = %d, want 20", sum.Iterations, sum.DroppedIterations, got)
			}
			if tt.vus == 1 && sum.DroppedIterations == 0 {
				t.Error("a 50ms iteration at a 10ms interval with one VU dropped nothing")
			}
			// Start k is never early.
			var all []time.Duration
			for _, v := range s.byVU {
				all = append(all, v...)
			}
			for _, at := range all {
				if at < 0 {
					t.Errorf("start at %v", at)
				}
			}
		})
	}
}

func TestArrivalRateScheduleDoesNotDrift(t *testing.T) {
	e := ConstantArrivalRate{Rate: 3, TimeUnit: time.Second, Duration: time.Second}
	for k, want := range []time.Duration{0, 333333333, 666666666} {
		if got := e.offset(int64(k)); got != want {
			t.Errorf("offset(%d) = %v, want %v", k, got, want)
		}
	}
	// Computed from k, not accumulated: the millionth start is exact.
	if got := e.offset(3_000_000); got != 1_000_000*time.Second {
		t.Errorf("offset(3e6) = %v", got)
	}
}

func TestScenariosRunConcurrentlyFromTheirStartTime(t *testing.T) {
	s := newStarts()
	ids := func(base int) NewVUFunc {
		inner := s.newVU(2 * time.Millisecond)
		return func(id int) (IterationFunc, error) { return inner(base + id) }
	}
	res := s.run(t,
		Scenario{Name: "a", Executor: ConstantVUs{VUs: 2, Duration: 100 * time.Millisecond}, GracefulStop: time.Second, NewVU: ids(0)},
		Scenario{Name: "b", Executor: ConstantVUs{VUs: 1, Duration: 50 * time.Millisecond}, StartTime: 80 * time.Millisecond, GracefulStop: time.Second, NewVU: ids(10)},
	)
	if len(s.byVU[0]) == 0 || len(s.byVU[1]) == 0 || len(s.byVU[10]) == 0 {
		t.Fatalf("not every VU ran: %v", s.byVU)
	}
	if first := s.byVU[10][0]; first < 80*time.Millisecond-time.Millisecond {
		t.Errorf("scenario b started at %v, before its startTime of 80ms", first)
	}
	if res.Elapsed < 130*time.Millisecond {
		t.Errorf("Elapsed = %v, want at least 130ms (b ends at 80+50ms)", res.Elapsed)
	}
}

func TestRunScenariosRejectsInvalidExecutors(t *testing.T) {
	newVU := shared(sleepIteration(time.Millisecond))
	for _, e := range []Executor{
		ConstantVUs{VUs: 0, Duration: time.Second},
		RampingVUs{},
		RampingVUs{Stages: []Stage{{time.Second, 0}}},
		ConstantArrivalRate{Rate: 0, TimeUnit: time.Second, Duration: time.Second, PreAllocatedVUs: 1},
	} {
		if _, err := RunScenarios(context.Background(), []Scenario{{Name: "x", Executor: e, NewVU: newVU}}); err == nil {
			t.Errorf("%+v: no error", e)
		}
	}
}

func TestScenarioCancellation(t *testing.T) {
	for _, e := range []Executor{
		ConstantVUs{VUs: 2, Duration: time.Minute},
		RampingVUs{Stages: []Stage{{time.Minute, 2}}},
		ConstantArrivalRate{Rate: 10, TimeUnit: time.Second, Duration: time.Minute, PreAllocatedVUs: 2},
	} {
		t.Run(fmt.Sprintf("%T", e), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			start := time.Now()
			if _, err := RunScenarios(ctx, []Scenario{{Name: "x", Executor: e, GracefulStop: time.Minute, NewVU: shared(sleepIteration(time.Hour))}}); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("RunScenarios took %v after cancel; goroutines did not stop", took)
			}
		})
	}
}
