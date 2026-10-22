package metrics

import (
	"errors"
	"fmt"
	"regexp"
	"sync/atomic"
	"time"
)

// Kind is the type of a metric family (ADR-015).
type Kind int

const (
	// Trend is a distribution of durations, like http_req_duration: an
	// ok and a failed histogram.
	Trend Kind = iota + 1
	// Counter is a total, like http_reqs.
	Counter
	// Rate is the fraction of true samples. For a *_failed family true
	// means failed, so its rate is the error rate, as http_req_failed.
	Rate
)

func (k Kind) String() string {
	switch k {
	case Trend:
		return "trend"
	case Counter:
		return "counter"
	case Rate:
		return "rate"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Def declares a metric family a protocol module records (ADR-015).
type Def struct {
	Name string // such as "grpc_req_duration"
	Kind Kind
}

// FamilyID is a family's index in its Families. Recording by ID costs a
// slice index, not a map lookup.
type FamilyID int

// familyName is <protocol>_<what>, in the style of the http_* names.
var familyName = regexp.MustCompile(`^[a-z][a-z0-9]*_[a-z0-9_]+$`)

// reservedNames are the Phase 1 metrics, which families must not shadow.
var reservedNames = map[string]bool{
	"http_reqs": true, "http_req_failed": true, "http_req_duration": true,
	"http_req_duration_successful": true, "http_protocols": true,
	"iterations": true, "dropped_iterations": true, "checks": true, "script_errors": true,
}

// Families holds the metric families of one run. Recorders record into it
// with atomics, spread over numShards shards like the HTTP histograms, so
// its memory does not depend on the VU count. It is created before the
// run and must not be modified afterwards.
type Families struct {
	fams []family
	ids  map[string]FamilyID
}

type family struct {
	def Def
	// trend holds a Trend's shards; nil for other kinds.
	trend []*shard
	// counts holds a Counter's totals, or a Rate's totals; trues holds
	// a Rate's true samples. One padded slot per shard avoids false
	// sharing.
	counts, trues []paddedCount
}

// paddedCount fills a cache line, so shards' counters do not share one.
type paddedCount struct {
	atomic.Int64
	_ [56]byte
}

// NewFamilies creates the families of a run. Names must be unique, follow
// the <protocol>_<what> pattern, and not be a Phase 1 metric.
func NewFamilies(defs []Def) (*Families, error) {
	f := &Families{fams: make([]family, len(defs)), ids: make(map[string]FamilyID, len(defs))}
	var errs []error
	for i, d := range defs {
		switch {
		case !familyName.MatchString(d.Name):
			errs = append(errs, fmt.Errorf("metric family %q: the name must be <protocol>_<what> in lower case", d.Name))
			continue
		case reservedNames[d.Name]:
			errs = append(errs, fmt.Errorf("metric family %q: the name is a built-in metric", d.Name))
			continue
		case d.Kind < Trend || d.Kind > Rate:
			errs = append(errs, fmt.Errorf("metric family %q: unknown kind %d", d.Name, int(d.Kind)))
			continue
		}
		if _, dup := f.ids[d.Name]; dup {
			errs = append(errs, fmt.Errorf("metric family %q is declared twice", d.Name))
			continue
		}
		f.ids[d.Name] = FamilyID(i)
		fam := family{def: d}
		switch d.Kind {
		case Trend:
			fam.trend = make([]*shard, numShards)
			for j := range fam.trend {
				fam.trend[j] = newShard()
			}
		case Counter:
			fam.counts = make([]paddedCount, numShards)
		case Rate:
			fam.counts = make([]paddedCount, numShards)
			fam.trues = make([]paddedCount, numShards)
		}
		f.fams[i] = fam
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return f, nil
}

// ID returns the ID of the family named name.
func (f *Families) ID(name string) (FamilyID, bool) {
	if f == nil {
		return 0, false
	}
	id, ok := f.ids[name]
	return id, ok
}

// Defs returns the families' definitions, in the order they were given.
func (f *Families) Defs() []Def {
	if f == nil {
		return nil
	}
	defs := make([]Def, len(f.fams))
	for i, fam := range f.fams {
		defs[i] = fam.def
	}
	return defs
}

// UseFamilies makes the recorder record families into f. A recorder
// without families (such as the throwaway one setup and teardown use)
// drops family samples, so they are never counted.
func (r *Recorder) UseFamilies(f *Families) {
	r.fams = f
}

// family returns the family id for recording, or nil when this recorder
// has no families. A kind mismatch is a bug in the protocol module.
func (r *Recorder) family(id FamilyID, kind Kind) *family {
	if r.fams == nil {
		return nil
	}
	fam := &r.fams.fams[id]
	if fam.def.Kind != kind {
		panic(fmt.Sprintf("metrics: family %q is a %s, not a %s", fam.def.Name, fam.def.Kind, kind))
	}
	return fam
}

// Trend records one duration in a Trend family, as ok or failed.
func (r *Recorder) Trend(id FamilyID, d time.Duration, ok bool) {
	fam := r.family(id, Trend)
	if fam == nil {
		return
	}
	s := fam.trend[r.shardIndex]
	if ok {
		s.ok.record(d)
	} else {
		s.failed.record(d)
	}
}

// Add adds n to a Counter family.
func (r *Recorder) Add(id FamilyID, n int64) {
	if fam := r.family(id, Counter); fam != nil {
		fam.counts[r.shardIndex].Add(n)
	}
}

// Rate records one true or false sample in a Rate family. For a
// *_failed family, v is true when the operation failed.
func (r *Recorder) Rate(id FamilyID, v bool) {
	fam := r.family(id, Rate)
	if fam == nil {
		return
	}
	fam.counts[r.shardIndex].Add(1)
	if v {
		fam.trues[r.shardIndex].Add(1)
	}
}

// FamilySummary is one family's merged result.
type FamilySummary struct {
	Name string
	Kind Kind
	// Count is a Trend's samples, a Counter's total, or a Rate's total.
	Count int
	// Failed is a Trend's failed samples.
	Failed int
	// Trues is a Rate's true samples; the rate is Trues/Count.
	Trues int
	// A Trend's distribution over every sample, failed ones included, as
	// http_req_duration. Zero when Count is 0.
	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration

	latency *combined
}

// Used reports whether the family recorded anything; reports show only
// used families.
func (s FamilySummary) Used() bool { return s.Count > 0 }

// Percentile returns a Trend's nearest-rank percentile p (0 < p <= 100),
// within ±0.78 %; ok is false when it has no samples.
func (s FamilySummary) Percentile(p float64) (d time.Duration, ok bool) {
	if s.latency == nil || p <= 0 || p > 100 {
		return 0, false
	}
	return s.latency.percentile(p), true
}

// Summarize merges every family, in the order they were declared. The
// recorders must no longer be written to.
func (f *Families) Summarize() []FamilySummary {
	if f == nil {
		return nil
	}
	out := make([]FamilySummary, len(f.fams))
	for i := range f.fams {
		fam := &f.fams[i]
		s := FamilySummary{Name: fam.def.Name, Kind: fam.def.Kind}
		switch fam.def.Kind {
		case Trend:
			var oks, faileds []*histogram
			for _, sh := range fam.trend {
				oks = append(oks, sh.ok)
				faileds = append(faileds, sh.failed)
			}
			all := combine(append(oks, faileds...))
			s.Count = int(all.n)
			s.Failed = int(combine(faileds).n)
			if all.n > 0 {
				s.latency = &all
				s.Min, s.Max = time.Duration(all.min), time.Duration(all.max)
				s.Mean = time.Duration(all.sum / all.n)
				s.P50, s.P90 = all.percentile(50), all.percentile(90)
				s.P95, s.P99 = all.percentile(95), all.percentile(99)
			}
		case Counter, Rate:
			for j := range fam.counts {
				s.Count += int(fam.counts[j].Load())
			}
			for j := range fam.trues {
				s.Trues += int(fam.trues[j].Load())
			}
		}
		out[i] = s
	}
	return out
}

// Family returns the summary of the family named name.
func (s Summary) Family(name string) (FamilySummary, bool) {
	for _, f := range s.Families {
		if f.Name == name {
			return f, true
		}
	}
	return FamilySummary{}, false
}
