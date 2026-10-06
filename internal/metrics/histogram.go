package metrics

import (
	"math"
	"math/bits"
	"sync/atomic"
	"time"
)

// Log-linear buckets (the HdrHistogram layout): values below 2^(subBits+1)
// ns get one bucket each; above that, every power of two is split into
// 2^subBits equal sub-buckets. A bucket is at most 1/64 of its lower bound
// wide, so reporting its midpoint is within ±0.78 % of any value in it.
const (
	subBits    = 6
	subBuckets = 1 << subBits // 64
	linear     = 2 * subBuckets
	// maxTracked is the largest latency with its own bucket; larger values
	// share the last bucket. Exact min and max are kept separately.
	maxTracked = int64(time.Hour)
)

// numBuckets is the bucket count needed to reach maxTracked.
var numBuckets = bucketIndex(maxTracked) + 1

// bucketIndex returns the bucket for v nanoseconds (v >= 0).
func bucketIndex(v int64) int {
	if v < linear {
		return int(v)
	}
	shift := bits.Len64(uint64(v)) - (subBits + 1)
	top := int(v >> shift) // in [subBuckets, 2*subBuckets)
	return linear + (shift-1)*subBuckets + (top - subBuckets)
}

// bucketMid returns the midpoint of bucket i in nanoseconds.
func bucketMid(i int) int64 {
	if i < linear {
		return int64(i)
	}
	shift := (i-linear)/subBuckets + 1
	top := int64(subBuckets + (i-linear)%subBuckets)
	lo := top << shift
	return lo + (int64(1)<<shift)/2
}

// histogram counts latencies in fixed buckets. All fields are updated with
// atomics so several VUs can share one histogram.
type histogram struct {
	counts []atomic.Uint64
	n      atomic.Uint64
	sum    atomic.Uint64 // nanoseconds
	min    atomic.Int64  // nanoseconds; math.MaxInt64 when empty
	max    atomic.Int64  // nanoseconds
}

func newHistogram() *histogram {
	h := &histogram{counts: make([]atomic.Uint64, numBuckets)}
	h.min.Store(math.MaxInt64)
	return h
}

func (h *histogram) record(d time.Duration) {
	v := max(int64(d), 0)
	h.counts[bucketIndex(min(v, maxTracked))].Add(1)
	h.n.Add(1)
	h.sum.Add(uint64(v))
	for cur := h.min.Load(); v < cur && !h.min.CompareAndSwap(cur, v); cur = h.min.Load() {
	}
	for cur := h.max.Load(); v > cur && !h.max.CompareAndSwap(cur, v); cur = h.max.Load() {
	}
}

// shard is the unit VUs share: one histogram for successful requests and
// one for failed requests that were sent.
type shard struct {
	ok, failed *histogram
}

func newShard() *shard {
	return &shard{ok: newHistogram(), failed: newHistogram()}
}

// combined is a merged, read-only view of several histograms.
type combined struct {
	counts   []uint64
	n, sum   uint64
	min, max int64
}

func combine(hs []*histogram) combined {
	c := combined{counts: make([]uint64, numBuckets), min: math.MaxInt64}
	for _, h := range hs {
		for i := range h.counts {
			c.counts[i] += h.counts[i].Load()
		}
		c.n += h.n.Load()
		c.sum += h.sum.Load()
		c.min = min(c.min, h.min.Load())
		c.max = max(c.max, h.max.Load())
	}
	return c
}

// percentile returns the nearest-rank percentile p (0 < p <= 100) as the
// midpoint of the bucket holding that rank, clamped to the exact min and
// max. c must not be empty.
func (c combined) percentile(p float64) time.Duration {
	// The tolerance absorbs float error: 99.9% of 1000 must be rank 999,
	// though 99.9*1000/100 computes to slightly more than 999.
	rank := max(uint64(math.Ceil(p*float64(c.n)/100-1e-9)), 1)
	var seen uint64
	for i, n := range c.counts {
		seen += n
		if seen >= rank {
			return time.Duration(min(max(bucketMid(i), c.min), c.max))
		}
	}
	return time.Duration(c.max)
}
