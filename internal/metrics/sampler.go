package metrics

import "time"

// Point is one interval of a run's time series (ADR-012): the requests
// that completed in (previous point, At], and how many VUs were active at
// At.
type Point struct {
	// At is the end of the interval, from the start of the test clock.
	At time.Duration
	// Requests and Failed count requests sent that completed in the
	// interval; Failed are the failed ones. Requests never sent (such as
	// invalid URLs) have no latency and are not in the series.
	Requests, Failed int
	// Mean and the percentiles cover every request in the interval,
	// failed ones included; zero when there were none. Percentiles are
	// within ±0.78 %.
	Mean, P50, P95, P99 time.Duration
	// VUs is how many VUs the scenarios had active at At (see the
	// executors); 0 once every scenario has stopped starting iterations.
	VUs int
}

// Sampler turns the cumulative histograms that a run's recorders share
// into per-interval points, by subtracting the previous snapshot. It adds
// nothing to the request path: VUs keep recording as usual, and Sample
// reads the shards with atomic loads.
//
// A snapshot is not atomic across buckets, so a request recorded while
// Sample runs may count in the next interval instead; totals over the run
// are unaffected. A Sampler is used by one goroutine.
type Sampler struct {
	shards []*shard
	// prev and cur are cumulative bucket counts (all requests sent), and
	// prevFailed/curFailed those of failed requests; prevSum is the
	// cumulative latency sum. Reused, so sampling allocates only the
	// returned point.
	prev, cur             []uint64
	prevFailed, curFailed uint64
	prevSum, curSum       uint64
}

// NewSampler returns a Sampler over the histograms recorders write to.
// Recorders from NewRecorders share a fixed set of shards; zero-value
// recorders, which create a shard on first use, are not supported.
func NewSampler(recorders []*Recorder) *Sampler {
	seen := make(map[*shard]bool)
	s := &Sampler{prev: make([]uint64, numBuckets), cur: make([]uint64, numBuckets)}
	for _, r := range recorders {
		if r.shard != nil && !seen[r.shard] {
			seen[r.shard] = true
			s.shards = append(s.shards, r.shard)
		}
	}
	return s
}

// Sample returns the point for the interval since the previous call (or
// since the start) ending at at, with vus active VUs.
func (s *Sampler) Sample(at time.Duration, vus int) Point {
	clear(s.cur)
	s.curFailed, s.curSum = 0, 0
	for _, sh := range s.shards {
		for _, h := range []*histogram{sh.ok, sh.failed} {
			for i := range h.counts {
				s.cur[i] += h.counts[i].Load()
			}
			s.curSum += h.sum.Load()
		}
		s.curFailed += sh.failed.n.Load()
	}

	// The interval's histogram, in prev's slice: prev is replaced by cur
	// below anyway.
	var n uint64
	for i := range s.cur {
		d := s.cur[i] - s.prev[i]
		s.prev[i] = d
		n += d
	}
	// Failed comes from a separate counter read at a slightly different
	// moment; clamp so an interval never shows more failures than requests.
	p := Point{At: at, Requests: int(n), Failed: int(min(s.curFailed-s.prevFailed, n)), VUs: vus}
	if n > 0 {
		interval := combined{counts: s.prev, n: n, min: 0, max: int64(maxTracked)}
		p.Mean = time.Duration((s.curSum - s.prevSum) / n)
		p.P50, p.P95, p.P99 = interval.percentile(50), interval.percentile(95), interval.percentile(99)
	}
	s.prev, s.cur = s.cur, s.prev
	// Failures clamped away above are carried into the next interval.
	s.prevFailed += uint64(p.Failed)
	s.prevSum = s.curSum
	return p
}
