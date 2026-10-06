package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// ConstantVUs runs VUs iterations back to back on every VU for Duration:
// the Phase 0 load model.
type ConstantVUs struct {
	VUs      int
	Duration time.Duration
}

func (e ConstantVUs) MaxVUs() int           { return e.VUs }
func (e ConstantVUs) Length() time.Duration { return e.Duration }

func (e ConstantVUs) activeAt(t time.Duration) int {
	if t < e.Duration {
		return e.VUs
	}
	return 0
}

func (e ConstantVUs) validate() error {
	if e.VUs < 1 || e.Duration <= 0 {
		return fmt.Errorf("constant-vus needs at least 1 VU and a positive duration, got %d VUs for %s", e.VUs, e.Duration)
	}
	return nil
}

func (e ConstantVUs) drive(s *scenarioRun, wg *sync.WaitGroup) {
	for i, iter := range s.iters {
		rec := s.recs[i]
		wg.Go(func() {
			if !waitUntil(s.ctx, s.start) {
				return
			}
			for s.ctx.Err() == nil && time.Now().Before(s.stopStarting) {
				s.iterate(s.ctx, rec, iter)
			}
		})
	}
}

// Stage is one step of a RampingVUs scenario: the number of active VUs
// moves linearly to Target over Duration. A zero Duration jumps at once.
type Stage struct {
	Duration time.Duration
	Target   int
}

// RampingVUs changes the number of active VUs over its stages, starting
// from StartVUs. At offset t, VU i (from 0) is active while the stage
// line's value at t is at least i+1, so VUs join and leave in order.
//
// There is no controller goroutine: each VU computes from the stages when
// it next becomes active and sleeps until then. A VU deactivated during
// an iteration may finish it for up to GracefulRampDown, then the
// iteration is cancelled.
type RampingVUs struct {
	StartVUs         int
	Stages           []Stage
	GracefulRampDown time.Duration
}

func (e RampingVUs) MaxVUs() int {
	n := e.StartVUs
	for _, st := range e.Stages {
		n = max(n, st.Target)
	}
	return n
}

func (e RampingVUs) Length() time.Duration {
	var d time.Duration
	for _, st := range e.Stages {
		d += st.Duration
	}
	return d
}

// maxRampingVUs keeps the stage arithmetic in int64: VUs times a duration
// in microseconds must not overflow.
const maxRampingVUs = 1_000_000

func (e RampingVUs) validate() error {
	if len(e.Stages) == 0 {
		return errors.New("ramping-vus needs at least one stage")
	}
	if e.StartVUs < 0 || e.GracefulRampDown < 0 {
		return errors.New("ramping-vus: startVUs and gracefulRampDown must not be negative")
	}
	for i, st := range e.Stages {
		if st.Duration < 0 || st.Target < 0 {
			return fmt.Errorf("ramping-vus: stage %d must have a non-negative duration and target", i+1)
		}
	}
	if e.Length() <= 0 {
		return errors.New("ramping-vus: the stages must last longer than 0s")
	}
	if e.MaxVUs() < 1 || e.MaxVUs() > maxRampingVUs {
		return fmt.Errorf("ramping-vus: needs between 1 and %d VUs, got %d", maxRampingVUs, e.MaxVUs())
	}
	if e.Length() > 1000*time.Hour {
		return errors.New("ramping-vus: the stages must not last longer than 1000h")
	}
	return nil
}

// next returns the first offset at or after t at which VU i is active
// (want true) or inactive (want false); ok is false if that never happens
// before the stages end. Crossings are exact in integer microseconds, and
// active(i, next(...)) always agrees with want, so a VU never wakes early
// and spins.
func (e RampingVUs) next(i int, t time.Duration, want bool) (at time.Duration, ok bool) {
	at, ok = e.crossing(i, t, want)
	if !ok || at >= e.Length() {
		return 0, false
	}
	return at, true
}

// crossing implements next without the end-of-stages bound.
func (e RampingVUs) crossing(i int, t time.Duration, want bool) (time.Duration, bool) {
	level := int64(i) + 1
	segStart, from := time.Duration(0), int64(e.StartVUs)
	for _, st := range e.Stages {
		to := int64(st.Target)
		segEnd := segStart + st.Duration
		// A zero-length stage only moves the starting value of the next
		// stage: several jumps at one instant leave the last one in
		// force, so no state at that instant is reported before them.
		if st.Duration > 0 && segEnd > t {
			lo := max(t, segStart)
			d := st.Duration.Microseconds()
			x := (lo - segStart).Microseconds()
			if (from*d+(to-from)*x >= level*d) == want {
				return lo, true
			}
			// Not in the wanted state at lo: it can only change by
			// crossing level inside this segment. A crossing at segEnd
			// belongs to the next segment, which may jump at once.
			var dx int64 = -1
			switch {
			case want && to >= level: // rising through level
				dx = ceilDiv((level-from)*d, to-from)
			case !want && to < level: // falling below level
				dx = (from-level)*d/(from-to) + 1
			}
			if dx >= 0 && dx < d {
				return segStart + time.Duration(dx)*time.Microsecond, true
			}
		}
		segStart, from = segEnd, to
	}
	return 0, false
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// activeAt is the number of active VUs at t: the floor of the stage
// line's value, in the same integer microseconds as next, so it agrees
// with when VUs actually join and leave. 0 once the stages have ended.
func (e RampingVUs) activeAt(t time.Duration) int {
	segStart, from := time.Duration(0), int64(e.StartVUs)
	for _, st := range e.Stages {
		to := int64(st.Target)
		if st.Duration > 0 && t < segStart+st.Duration {
			d := st.Duration.Microseconds()
			x := (t - segStart).Microseconds()
			return int((from*d + (to-from)*x) / d) // values are >= 0, so this is the floor
		}
		segStart, from = segStart+st.Duration, to
	}
	return 0
}

func (e RampingVUs) drive(s *scenarioRun, wg *sync.WaitGroup) {
	length := e.Length()
	hardStop, _ := s.ctx.Deadline()
	for i, iter := range s.iters {
		rec := s.recs[i]
		wg.Go(func() {
			for s.ctx.Err() == nil {
				now := time.Since(s.start)
				if now >= length {
					return
				}
				on, ok := e.next(i, max(now, 0), true)
				if !ok || on >= length {
					return // never active again
				}
				if on > now {
					if !waitUntil(s.ctx, s.start.Add(on)) {
						return
					}
					continue // re-check: time has moved on
				}
				e.runIteration(s, rec, iter, i, now, length, hardStop)
			}
		})
	}
}

// runIteration runs one iteration of an active VU. If the VU is due to be
// deactivated before the scenario ends, the iteration gets a deadline of
// that moment plus GracefulRampDown; otherwise the scenario's own graceful
// stop applies. The extra context is created only during ramp-downs.
func (e RampingVUs) runIteration(s *scenarioRun, rec *metrics.Recorder, iter IterationFunc,
	i int, now, length time.Duration, hardStop time.Time) {
	ctx := s.ctx
	if off, ok := e.next(i, now, false); ok && off < length {
		if deadline := s.start.Add(off + e.GracefulRampDown); deadline.Before(hardStop) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(s.ctx, deadline)
			defer cancel()
		}
	}
	s.iterate(ctx, rec, iter)
}

// ConstantArrivalRate starts Rate iterations per TimeUnit for Duration,
// whatever the VUs' iteration time. One scheduler goroutine computes start
// k as start + k*TimeUnit/Rate, so the rate does not drift, and hands each
// start to an idle VU over an unbuffered channel without blocking. If no
// VU is idle the start is dropped and counted, not queued. If the
// scheduler wakes late, the starts that are due go out at once.
type ConstantArrivalRate struct {
	Rate            int
	TimeUnit        time.Duration
	Duration        time.Duration
	PreAllocatedVUs int
}

func (e ConstantArrivalRate) MaxVUs() int           { return e.PreAllocatedVUs }
func (e ConstantArrivalRate) Length() time.Duration { return e.Duration }

// activeAt is the VU pool while the scenario runs; how busy it was shows
// in dropped iterations.
func (e ConstantArrivalRate) activeAt(t time.Duration) int {
	if t < e.Duration {
		return e.PreAllocatedVUs
	}
	return 0
}

func (e ConstantArrivalRate) validate() error {
	if e.Rate < 1 || e.TimeUnit <= 0 || e.Duration <= 0 || e.PreAllocatedVUs < 1 {
		return fmt.Errorf("constant-arrival-rate needs rate >= 1, a positive timeUnit and duration and preAllocatedVUs >= 1, got rate %d per %s for %s with %d VUs",
			e.Rate, e.TimeUnit, e.Duration, e.PreAllocatedVUs)
	}
	return nil
}

// offset is when start k is due, relative to the scenario's start.
func (e ConstantArrivalRate) offset(k int64) time.Duration {
	return time.Duration(k * int64(e.TimeUnit) / int64(e.Rate))
}

func (e ConstantArrivalRate) drive(s *scenarioRun, wg *sync.WaitGroup) {
	starts := make(chan struct{})
	wg.Go(func() {
		defer close(starts) // idle VUs then return
		if !waitUntil(s.ctx, s.start) {
			return
		}
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		for k := int64(0); ; k++ {
			at := s.start.Add(e.offset(k))
			if !at.Before(s.stopStarting) {
				return
			}
			if d := time.Until(at); d > 0 {
				timer.Reset(d)
				select {
				case <-timer.C:
				case <-s.ctx.Done():
					return
				}
			}
			select {
			case starts <- struct{}{}:
			default:
				s.dropped.Add(1)
			}
		}
	})
	for i, iter := range s.iters {
		rec := s.recs[i]
		wg.Go(func() {
			for {
				select {
				case _, ok := <-starts:
					if !ok {
						return
					}
					s.iterate(s.ctx, rec, iter)
				case <-s.ctx.Done():
					return
				}
			}
		})
	}
}
