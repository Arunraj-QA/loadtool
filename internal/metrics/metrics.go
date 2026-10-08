// Package metrics records request outcomes and aggregates them into a summary.
//
// Latencies go into fixed-size, log-bucketed histograms (see histogram.go),
// so memory does not grow with test length or request rate. Percentiles are
// within ±0.78 % of the exact value; counts, min, max and mean are exact.
//
// VUs share a small number of histogram shards and record into them with
// atomic operations; per-VU counters that are rarely written stay in the
// Recorder itself.
package metrics

import (
	"time"
)

// numShards is how many histogram shards VUs are spread over. It keeps
// atomic updates mostly uncontended while total memory stays at about
// numShards * 2 * 19 KB, independent of the VU count.
const numShards = 16

// Recorder collects results for a single VU. Each Recorder must be used by
// one goroutine at a time; Recorders created by NewRecorders share
// histogram shards safely.
//
// The zero value is ready to use and gets a private shard on first use.
type Recorder struct {
	shard *shard
	// unsent counts failed requests that were never sent (for example an
	// invalid URL); they have no latency.
	unsent int

	scriptErrors     int
	firstScriptError string
	iterations       int
	protocols        Protocols

	// checkNames is shared by a run's recorders; checkIndex caches its
	// indexes for this recorder, and checks is indexed by them.
	checkNames *checkNames
	checkIndex map[string]int
	checks     []checkCount

	// fams receives protocol metric families (ADR-015); nil drops them.
	// shardIndex picks this recorder's family shard.
	fams       *Families
	shardIndex int
}

// NewRecorders returns n Recorders spread over a fixed set of shared
// histogram shards.
func NewRecorders(n int) []*Recorder {
	shards := make([]*shard, min(n, numShards))
	for i := range shards {
		shards[i] = newShard()
	}
	names := newCheckNames()
	recs := make([]*Recorder, n)
	for i := range recs {
		recs[i] = &Recorder{shard: shards[i%len(shards)], checkNames: names, shardIndex: i % numShards}
	}
	return recs
}

// Record stores the latency and outcome of one request that was sent.
func (r *Recorder) Record(latency time.Duration, ok bool) {
	if r.shard == nil {
		r.shard = newShard()
	}
	if ok {
		r.shard.ok.record(latency)
	} else {
		r.shard.failed.record(latency)
	}
}

// RecordUnsent counts a failed request that was never sent. It adds no
// latency sample, so it cannot distort the percentiles.
func (r *Recorder) RecordUnsent() {
	r.unsent++
}

// RecordProtocol counts the protocol of a response received, as the
// response reports it ("HTTP/1.1", "HTTP/2.0").
func (r *Recorder) RecordProtocol(proto string) {
	switch proto {
	case "HTTP/2.0":
		r.protocols.HTTP2++
	case "HTTP/1.1", "HTTP/1.0":
		r.protocols.HTTP1++
	default:
		r.protocols.Other++
	}
}

// Protocols counts responses by HTTP version (ADR-010), so a run shows
// whether "auto" really negotiated HTTP/2.
type Protocols struct {
	HTTP1, HTTP2, Other int
}

// RecordIteration counts an iteration that ran to its end, with or without
// a script error. Iterations cut short by the end of the test are not
// counted.
func (r *Recorder) RecordIteration() {
	r.iterations++
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
	// Min, Mean and Max are exact; percentiles are within ±0.78 %.
	// Zero when no request was sent.
	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration

	// Latency over successful requests only, within ±0.78 %. Zero when none
	// succeeded.
	SuccessP50, SuccessP90, SuccessP95, SuccessP99 time.Duration

	// ScriptErrors counts iterations that ended with a script error.
	ScriptErrors int
	// FirstScriptError is one example message, empty if there were none.
	FirstScriptError string

	// Checks are the check() results in the order they were first run.
	Checks []CheckResult

	// Iterations counts iterations that ran to their end.
	Iterations int
	// Protocols counts responses by HTTP version; requests without a
	// response are not counted.
	Protocols Protocols

	// DroppedIterations counts arrival-rate starts no VU was free for.
	// The engine sets it; recorders do not track it.
	DroppedIterations int

	// Families are the protocol metric families (ADR-015), in declaration
	// order; nil when the run has none. The runner sets them from its
	// Families; Merge does not.
	Families []FamilySummary

	// latency is the merged histogram of every request sent; nil when
	// none was. It backs Percentile.
	latency *combined
}

// Percentile returns the nearest-rank latency percentile p (0 < p <= 100)
// over every request sent, within ±0.78 %. ok is false when no request
// was sent.
func (s Summary) Percentile(p float64) (d time.Duration, ok bool) {
	if s.latency == nil || p <= 0 || p > 100 {
		return 0, false
	}
	return s.latency.percentile(p), true
}

// Merge aggregates recorders into a Summary. The recorders must no longer
// be written to while Merge runs.
func Merge(recorders []*Recorder) Summary {
	var s Summary
	seen := make(map[*shard]bool)
	var okHists, failedHists []*histogram
	for _, r := range recorders {
		s.Failures += r.unsent
		s.ScriptErrors += r.scriptErrors
		s.Iterations += r.iterations
		s.Protocols.HTTP1 += r.protocols.HTTP1
		s.Protocols.HTTP2 += r.protocols.HTTP2
		s.Protocols.Other += r.protocols.Other
		if s.FirstScriptError == "" {
			s.FirstScriptError = r.firstScriptError
		}
		if r.shard != nil && !seen[r.shard] {
			seen[r.shard] = true
			okHists = append(okHists, r.shard.ok)
			failedHists = append(failedHists, r.shard.failed)
		}
	}

	s.Checks = mergeChecks(recorders)

	ok := combine(okHists)
	all := combine(append(okHists, failedHists...))
	s.Successes = int(ok.n)
	s.Sent = int(all.n)
	s.Failures += s.Sent - s.Successes
	s.Requests = s.Successes + s.Failures
	if s.Requests > 0 {
		s.ErrorRate = float64(s.Failures) / float64(s.Requests)
	}

	if ok.n > 0 {
		s.SuccessP50 = ok.percentile(50)
		s.SuccessP90 = ok.percentile(90)
		s.SuccessP95 = ok.percentile(95)
		s.SuccessP99 = ok.percentile(99)
	}
	if all.n == 0 {
		return s
	}
	s.latency = &all
	s.Min = time.Duration(all.min)
	s.Max = time.Duration(all.max)
	s.Mean = time.Duration(all.sum / all.n)
	s.P50 = all.percentile(50)
	s.P90 = all.percentile(90)
	s.P95 = all.percentile(95)
	s.P99 = all.percentile(99)
	return s
}
