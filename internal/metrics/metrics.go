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
//
// Successful and failed latencies are kept apart so the summary can report
// both all-request and success-only percentiles while storing each sample
// once.
type Recorder struct {
	okLatencies     []time.Duration
	failedLatencies []time.Duration
	// unsent counts failed requests that were never sent (for example an
	// invalid URL); they have no latency.
	unsent int

	scriptErrors     int
	firstScriptError string
}

// Record stores the latency and outcome of one request that was sent.
func (r *Recorder) Record(latency time.Duration, ok bool) {
	if ok {
		r.okLatencies = append(r.okLatencies, latency)
	} else {
		r.failedLatencies = append(r.failedLatencies, latency)
	}
}

// RecordUnsent counts a failed request that was never sent. It adds no
// latency sample, so it cannot distort the percentiles.
func (r *Recorder) RecordUnsent() {
	r.unsent++
}

// RecordScriptError counts an iteration that ended with a script error.
// The first message is kept so the summary can show an example.
func (r *Recorder) RecordScriptError(msg string) {
	if r.scriptErrors == 0 {
		r.firstScriptError = msg
	}
	r.scriptErrors++
}

// Summary is the aggregated result of a test run.
type Summary struct {
	Requests  int
	Successes int
	Failures  int
	// Sent counts requests that were sent and so have a latency: Requests
	// minus failures that never left LoadTool (such as invalid URLs).
	Sent int
	// ErrorRate is Failures/Requests in the range [0, 1].
	ErrorRate float64

	// Latency over every request that was sent, failed ones included (the
	// same population k6's http_req_duration and JMeter's elapsed use).
	// Zero when no request was sent.
	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration

	// Latency over successful requests only. Zero when none succeeded.
	SuccessP50, SuccessP90, SuccessP95, SuccessP99 time.Duration

	// ScriptErrors counts iterations that ended with a script error.
	ScriptErrors int
	// FirstScriptError is one example message, empty if there were none.
	FirstScriptError string
}

// Merge aggregates recorders into a Summary. The recorders must no longer
// be written to while Merge runs.
//
// It sorts one copy of the successful and one of the failed latencies and
// selects all-request percentiles from the two sorted slices directly, so
// it never holds a third, merged copy.
func Merge(recorders []*Recorder) Summary {
	var s Summary
	nOK, nFailed := 0, 0
	for _, r := range recorders {
		nOK += len(r.okLatencies)
		nFailed += len(r.failedLatencies)
		s.Failures += r.unsent
		s.ScriptErrors += r.scriptErrors
		if s.FirstScriptError == "" {
			s.FirstScriptError = r.firstScriptError
		}
	}
	s.Successes = nOK
	s.Failures += nFailed
	s.Requests = s.Successes + s.Failures
	if s.Requests > 0 {
		s.ErrorRate = float64(s.Failures) / float64(s.Requests)
	}

	ok := make([]time.Duration, 0, nOK)
	failed := make([]time.Duration, 0, nFailed)
	for _, r := range recorders {
		ok = append(ok, r.okLatencies...)
		failed = append(failed, r.failedLatencies...)
	}
	slices.Sort(ok)
	slices.Sort(failed)

	if len(ok) > 0 {
		s.SuccessP50 = percentile(ok, nil, 50)
		s.SuccessP90 = percentile(ok, nil, 90)
		s.SuccessP95 = percentile(ok, nil, 95)
		s.SuccessP99 = percentile(ok, nil, 99)
	}

	sent := len(ok) + len(failed)
	s.Sent = sent
	if sent == 0 {
		return s
	}
	var sum time.Duration
	for _, d := range ok {
		sum += d
	}
	for _, d := range failed {
		sum += d
	}
	s.Mean = sum / time.Duration(sent)
	s.Min = kth(ok, failed, 0)
	s.Max = kth(ok, failed, sent-1)
	s.P50 = percentile(ok, failed, 50)
	s.P90 = percentile(ok, failed, 90)
	s.P95 = percentile(ok, failed, 95)
	s.P99 = percentile(ok, failed, 99)
	return s
}

// percentile returns the nearest-rank percentile p (0 < p <= 100) of the
// union of two ascending slices, which must not both be empty.
func percentile(a, b []time.Duration, p int) time.Duration {
	n := len(a) + len(b)
	// Nearest rank: ceil(p/100 * n), 1-based.
	rank := (p*n + 99) / 100
	return kth(a, b, max(rank, 1)-1)
}

// kth returns the k-th smallest (0-based) element of the union of two
// ascending slices, with 0 <= k < len(a)+len(b), in O(log n) time.
func kth(a, b []time.Duration, k int) time.Duration {
	if len(a) > len(b) {
		a, b = b, a
	}
	// Take i elements from a and j = k+1-i from b; find the i for which
	// those are exactly the k+1 smallest elements.
	n := k + 1
	lo, hi := max(0, n-len(b)), min(n, len(a))
	for {
		i := (lo + hi) / 2
		j := n - i
		switch {
		case i < len(a) && j > 0 && b[j-1] > a[i]:
			lo = i + 1 // a[i] belongs to the smallest n: take more from a
		case i > 0 && j < len(b) && a[i-1] > b[j]:
			hi = i - 1 // a[i-1] does not: take fewer from a
		case i == 0:
			return b[j-1]
		case j == 0:
			return a[i-1]
		default:
			return max(a[i-1], b[j-1])
		}
	}
}
