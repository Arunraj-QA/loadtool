// Package thresholds parses a script's threshold expressions and evaluates
// them against a run's merged metrics (ADR-008):
//
//	thresholds: { http_req_duration: ["p(95)<500", "avg<200"], checks: ["rate>0.99"] }
//
// An expression is "<aggregate> <op> <number>". Expressions are parsed
// when options are read, so a typo fails before any load is generated;
// they are evaluated once, at the end, against the same metrics.Summary
// the console summary prints, so the two always agree.
package thresholds

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// Unit says how a metric's values are measured, for display.
type Unit int

const (
	Milliseconds Unit = iota // durations
	Fraction                 // a rate between 0 and 1
	PerSecond                // a rate per second
	Count                    // a whole number
)

// metric describes a metric thresholds can use.
type metric struct {
	aggregates []string // allowed aggregates; "p" stands for p(N)
	// value returns the aggregate's value; ok is false when the metric has
	// no data (for example no requests were sent).
	value func(agg string, p float64, s metrics.Summary, elapsed time.Duration) (v float64, ok bool)
	unit  func(agg string) Unit
}

var known = map[string]metric{
	"http_req_duration": {
		aggregates: []string{"avg", "min", "max", "med", "p"},
		value: func(agg string, p float64, s metrics.Summary, _ time.Duration) (float64, bool) {
			if s.Sent == 0 {
				return 0, false
			}
			var d time.Duration
			switch agg {
			case "avg":
				d = s.Mean
			case "min":
				d = s.Min
			case "max":
				d = s.Max
			case "med":
				d, _ = s.Percentile(50)
			case "p":
				d, _ = s.Percentile(p)
			}
			return float64(d) / float64(time.Millisecond), true
		},
		unit: func(string) Unit { return Milliseconds },
	},
	"http_req_failed": {
		aggregates: []string{"rate"},
		value: func(_ string, _ float64, s metrics.Summary, _ time.Duration) (float64, bool) {
			return s.ErrorRate, s.Requests > 0
		},
		unit: func(string) Unit { return Fraction },
	},
	"checks": {
		aggregates: []string{"rate"},
		value: func(_ string, _ float64, s metrics.Summary, _ time.Duration) (float64, bool) {
			var passes, total int
			for _, c := range s.Checks {
				passes += c.Passes
				total += c.Passes + c.Fails
			}
			if total == 0 {
				return 0, false
			}
			return float64(passes) / float64(total), true
		},
		unit: func(string) Unit { return Fraction },
	},
	"http_reqs":          counter(func(s metrics.Summary) int { return s.Requests }),
	"iterations":         counter(func(s metrics.Summary) int { return s.Iterations }),
	"dropped_iterations": counter(func(s metrics.Summary) int { return s.DroppedIterations }),
}

// counter is a metric with a total (count) and a per-second rate. A count
// of zero is data, not "no data".
func counter(n func(metrics.Summary) int) metric {
	return metric{
		aggregates: []string{"count", "rate"},
		value: func(agg string, _ float64, s metrics.Summary, elapsed time.Duration) (float64, bool) {
			if agg == "count" {
				return float64(n(s)), true
			}
			if elapsed <= 0 {
				return 0, false
			}
			return float64(n(s)) / elapsed.Seconds(), true
		},
		unit: func(agg string) Unit {
			if agg == "count" {
				return Count
			}
			return PerSecond
		},
	}
}

// Threshold is one parsed expression.
type Threshold struct {
	Metric string
	// Expr is the expression as the script wrote it.
	Expr string

	agg   string  // avg, min, max, med, p, count or rate
	p     float64 // the N of p(N)
	op    string
	limit float64
}

var exprPattern = regexp.MustCompile(`^\s*(avg|min|max|med|count|rate|p\(\s*([0-9]+(?:\.[0-9]+)?)\s*\))\s*(<=|>=|==|!=|<|>)\s*(-?[0-9]+(?:\.[0-9]+)?)\s*$`)

// Parse parses every expression of every metric. Thresholds are returned
// by metric name, then in the order the script wrote them. All problems
// are reported together, each naming its metric and expression.
func Parse(defs map[string][]string) ([]Threshold, error) {
	var out []Threshold
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(defs)) {
		m, ok := known[name]
		if !ok {
			errs = append(errs, unknownMetric(name))
			continue
		}
		for _, expr := range defs[name] {
			t, err := parseExpr(name, m, expr)
			if err != nil {
				errs = append(errs, fmt.Errorf("options.thresholds.%s: %q: %w", name, expr, err))
				continue
			}
			out = append(out, t)
		}
	}
	return out, errors.Join(errs...)
}

func unknownMetric(name string) error {
	if strings.Contains(name, "{") {
		return fmt.Errorf("options.thresholds: %q: thresholds on sub-metrics (tags) are not supported yet", name)
	}
	return fmt.Errorf("options.thresholds: unknown metric %q; supported metrics are %s",
		name, strings.Join(slices.Sorted(maps.Keys(known)), ", "))
}

func parseExpr(name string, m metric, expr string) (Threshold, error) {
	g := exprPattern.FindStringSubmatch(expr)
	if g == nil {
		return Threshold{}, errors.New(`expected "<aggregate> <op> <number>", such as "p(95)<500" or "rate<0.01"`)
	}
	t := Threshold{Metric: name, Expr: expr, agg: g[1], op: g[3]}
	if strings.HasPrefix(g[1], "p(") {
		t.agg = "p"
		t.p, _ = strconv.ParseFloat(g[2], 64)
		if t.p <= 0 || t.p > 100 {
			return Threshold{}, fmt.Errorf("percentile must be above 0 and at most 100, got %s", g[2])
		}
	}
	if !slices.Contains(m.aggregates, t.agg) {
		return Threshold{}, fmt.Errorf("%s does not support %s; use %s", name, aggName(t.agg), aggList(m.aggregates))
	}
	t.limit, _ = strconv.ParseFloat(g[4], 64)
	return t, nil
}

func aggName(agg string) string {
	if agg == "p" {
		return "p(N)"
	}
	return agg
}

func aggList(aggs []string) string {
	names := make([]string, len(aggs))
	for i, a := range aggs {
		names[i] = aggName(a)
	}
	return strings.Join(names, ", ")
}

// Result is the outcome of one threshold.
type Result struct {
	Threshold
	// Observed is the metric's value; meaningless when NoData is set.
	Observed float64
	Unit     Unit
	// NoData means the metric had no samples. Such a threshold fails: a
	// threshold that could not be checked must not pass silently.
	NoData bool
	Passed bool
	// Approximate marks a percentile so close to the limit that the
	// histogram's ±0.78 % error could change the outcome.
	Approximate bool
}

// histogramError is the relative error of percentiles (ADR-004).
const histogramError = 0.0078

// Evaluate checks every threshold against s; elapsed is the load phase's
// length, for per-second rates.
func Evaluate(ts []Threshold, s metrics.Summary, elapsed time.Duration) []Result {
	out := make([]Result, len(ts))
	for i, t := range ts {
		m := known[t.Metric]
		r := Result{Threshold: t, Unit: m.unit(t.agg)}
		v, ok := m.value(t.agg, t.p, s, elapsed)
		if !ok {
			r.NoData = true
		} else {
			r.Observed = v
			r.Passed = compare(v, t.op, t.limit)
			if t.agg == "p" || t.agg == "med" {
				r.Approximate = math.Abs(v-t.limit) <= histogramError*math.Abs(t.limit)
			}
		}
		out[i] = r
	}
	return out
}

func compare(v float64, op string, limit float64) bool {
	switch op {
	case "<":
		return v < limit
	case "<=":
		return v <= limit
	case ">":
		return v > limit
	case ">=":
		return v >= limit
	case "==":
		return v == limit
	default: // "!="
		return v != limit
	}
}

// Failed reports whether any threshold failed.
func Failed(rs []Result) bool {
	return slices.ContainsFunc(rs, func(r Result) bool { return !r.Passed })
}
