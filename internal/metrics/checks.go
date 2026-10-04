package metrics

import "sync"

// checkNames assigns each check name an index on first use, so all VUs
// count the same check in the same slot and the summary can list checks
// in the order they were first run. It is shared by a run's recorders;
// each recorder caches the indexes it has seen, so the lock is taken once
// per name per VU, not per check.
type checkNames struct {
	mu    sync.Mutex
	names []string
	index map[string]int
}

func newCheckNames() *checkNames {
	return &checkNames{index: make(map[string]int)}
}

func (c *checkNames) id(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i, ok := c.index[name]; ok {
		return i
	}
	c.index[name] = len(c.names)
	c.names = append(c.names, name)
	return len(c.names) - 1
}

func (c *checkNames) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.names...)
}

// checkCount is one recorder's tally for one check.
type checkCount struct {
	passes, fails int
	firstError    string
}

// CheckResult is the merged tally of one check.
type CheckResult struct {
	Name   string
	Passes int
	Fails  int
	// FirstError is the first message thrown by the check's condition,
	// empty if it never threw.
	FirstError string
}

// RecordCheck counts one run of the check called name. errMsg is set when
// the condition threw; that run counts as failed.
func (r *Recorder) RecordCheck(name string, passed bool, errMsg string) {
	if r.checkNames == nil {
		r.checkNames = newCheckNames()
	}
	i, ok := r.checkIndex[name]
	if !ok {
		if r.checkIndex == nil {
			r.checkIndex = make(map[string]int)
		}
		i = r.checkNames.id(name)
		r.checkIndex[name] = i
	}
	if i >= len(r.checks) {
		r.checks = append(r.checks, make([]checkCount, i+1-len(r.checks))...)
	}
	c := &r.checks[i]
	if passed {
		c.passes++
		return
	}
	c.fails++
	if c.firstError == "" {
		c.firstError = errMsg
	}
}

// mergeChecks sums the recorders' checks in first-run order. Recorders
// from NewRecorders share one name table; a zero-value recorder has its
// own, and its checks are merged by name.
func mergeChecks(recorders []*Recorder) []CheckResult {
	var out []CheckResult
	pos := make(map[string]int)
	tables := make(map[*checkNames][]string)
	// List every name in its table's order first, so the output follows
	// first-run order across the whole run, not recorder order.
	for _, r := range recorders {
		if r.checkNames == nil || tables[r.checkNames] != nil {
			continue
		}
		names := r.checkNames.list()
		tables[r.checkNames] = names
		for _, name := range names {
			if _, ok := pos[name]; !ok {
				pos[name] = len(out)
				out = append(out, CheckResult{Name: name})
			}
		}
	}
	for _, r := range recorders {
		names := tables[r.checkNames]
		for i, c := range r.checks {
			j := pos[names[i]]
			out[j].Passes += c.passes
			out[j].Fails += c.fails
			if out[j].FirstError == "" {
				out[j].FirstError = c.firstError
			}
		}
	}
	return out
}
