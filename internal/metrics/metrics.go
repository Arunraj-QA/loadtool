// Package metrics records request outcomes and aggregates them into a summary.
//
// Each VU owns one Recorder, so the recording hot path needs no locks or
// atomics. Recorders are merged once, after all VUs have stopped.
package metrics

import (
	"slices"
	"time"
)

// Recorder collects results for a single VU. It is not safe for concurrent
// use: give every goroutine its own Recorder.
type Recorder struct {
	latencies []time.Duration
	failures  int
}

// Record stores the latency and outcome of one completed request.
func (r *Recorder) Record(latency time.Duration, ok bool) {
	r.latencies = append(r.latencies, latency)
	if !ok {
		r.failures++
	}
}

// Summary is the aggregated result of a test run.
type Summary struct {
	Requests  int
	Successes int
	Failures  int
	// ErrorRate is Failures/Requests in the range [0, 1].
	ErrorRate float64

	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration
}

// Merge aggregates recorders into a Summary. The recorders must no longer
// be written to while Merge runs.
func Merge(recorders []*Recorder) Summary {
	var s Summary
	total := 0
	for _, r := range recorders {
		total += len(r.latencies)
		s.Failures += r.failures
	}
	if total == 0 {
		return s
	}

	all := make([]time.Duration, 0, total)
	for _, r := range recorders {
		all = append(all, r.latencies...)
	}
	slices.Sort(all)

	var sum time.Duration
	for _, d := range all {
		sum += d
	}

	s.Requests = total
	s.Successes = total - s.Failures
	s.ErrorRate = float64(s.Failures) / float64(total)
	s.Min = all[0]
	s.Max = all[total-1]
	s.Mean = sum / time.Duration(total)
	s.P50 = percentile(all, 50)
	s.P90 = percentile(all, 90)
	s.P95 = percentile(all, 95)
	s.P99 = percentile(all, 99)
	return s
}

// percentile returns the nearest-rank percentile p (0 < p <= 100) of a
// non-empty, ascending slice.
func percentile(sorted []time.Duration, p int) time.Duration {
	// Nearest rank: ceil(p/100 * n), 1-based.
	rank := (p*len(sorted) + 99) / 100
	return sorted[max(rank, 1)-1]
}
