package report

import (
	"encoding/json"
	"io"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

// SchemaVersion is the version of the JSON summary format. Adding fields
// keeps it; removing, renaming or changing the meaning of a field bumps it
// (docs/json-summary.md).
const SchemaVersion = 1

// jsonSummary is the JSON summary document, version 1. Durations are
// milliseconds; rates are fractions (0–1) or per second, as named.
type jsonSummary struct {
	SchemaVersion int      `json:"schemaVersion"`
	Tool          jsonTool `json:"tool"`
	Script        string   `json:"script"`
	// Status is "completed" or "interrupted".
	Status string `json:"status"`
	// StartedAt is when the test clock started, RFC 3339 with
	// milliseconds, UTC; empty if unknown.
	StartedAt string  `json:"startedAt"`
	ElapsedMs float64 `json:"elapsedMs"`
	// TeardownError is the teardown failure message, empty if none.
	TeardownError string `json:"teardownError"`

	Config     jsonConfig      `json:"config"`
	Metrics    jsonMetrics     `json:"metrics"`
	Checks     []jsonCheck     `json:"checks"`
	Thresholds []jsonThreshold `json:"thresholds"`
	// Series was added to schema version 1 in Phase 1 step 11 (an
	// additive change).
	Series []jsonPoint `json:"series"`
}

// jsonPoint is one second of the time series (ADR-012): requests that
// completed in the interval ending at AtMs.
type jsonPoint struct {
	AtMs          float64    `json:"atMs"`
	VUs           int        `json:"vus"`
	HTTPReqs      int        `json:"http_reqs"`
	HTTPReqFailed int        `json:"http_req_failed"`
	Duration      *jsonPtDur `json:"http_req_duration"`
}

type jsonPtDur struct {
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type jsonTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type jsonConfig struct {
	// VUs is the total across scenarios; DurationMs is when the last
	// scenario stops starting iterations.
	VUs        int            `json:"vus"`
	DurationMs float64        `json:"durationMs"`
	Scenarios  []jsonScenario `json:"scenarios"`
}

// jsonScenario holds the fields of its executor; the others are omitted.
type jsonScenario struct {
	Name           string  `json:"name"`
	Executor       string  `json:"executor"`
	Exec           string  `json:"exec"`
	StartTimeMs    float64 `json:"startTimeMs"`
	GracefulStopMs float64 `json:"gracefulStopMs"`

	VUs                *int        `json:"vus,omitempty"`
	DurationMs         *float64    `json:"durationMs,omitempty"`
	StartVUs           *int        `json:"startVUs,omitempty"`
	Stages             []jsonStage `json:"stages,omitempty"`
	GracefulRampDownMs *float64    `json:"gracefulRampDownMs,omitempty"`
	Rate               *int        `json:"rate,omitempty"`
	TimeUnitMs         *float64    `json:"timeUnitMs,omitempty"`
	PreAllocatedVUs    *int        `json:"preAllocatedVUs,omitempty"`
}

type jsonStage struct {
	DurationMs float64 `json:"durationMs"`
	Target     int     `json:"target"`
}

// jsonMetrics uses the threshold metric names (ADR-008).
type jsonMetrics struct {
	HTTPReqs          jsonCounter   `json:"http_reqs"`
	HTTPReqFailed     jsonFailed    `json:"http_req_failed"`
	HTTPReqDuration   *jsonTrend    `json:"http_req_duration"`
	SuccessDuration   *jsonTrend    `json:"http_req_duration_successful"`
	Iterations        jsonCounter   `json:"iterations"`
	DroppedIterations jsonCounter   `json:"dropped_iterations"`
	Checks            jsonRate      `json:"checks"`
	ScriptErrors      jsonScriptErr `json:"script_errors"`
}

type jsonCounter struct {
	Count int `json:"count"`
	// Rate is per second of elapsed time.
	Rate float64 `json:"rate"`
}

// jsonRate is a fraction: Passes of Count; Rate is Passes/Count, null
// when Count is 0.
type jsonRate struct {
	Rate   *float64 `json:"rate"`
	Passes int      `json:"passes"`
	Fails  int      `json:"fails"`
	Count  int      `json:"count"`
}

// jsonTrend is a latency distribution in milliseconds. Percentiles are
// within ±0.78 % (ADR-004); min, max and avg are exact. Fields the
// distribution does not have are omitted.
type jsonTrend struct {
	Count int      `json:"count"`
	Min   *float64 `json:"min,omitempty"`
	Avg   *float64 `json:"avg,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	P50   float64  `json:"p50"`
	P90   float64  `json:"p90"`
	P95   float64  `json:"p95"`
	P99   float64  `json:"p99"`
}

// jsonFailed is http_req_failed: Rate is Failed/Count, null when Count
// is 0.
type jsonFailed struct {
	Rate   *float64 `json:"rate"`
	Failed int      `json:"failed"`
	Count  int      `json:"count"`
}

type jsonScriptErr struct {
	Count int    `json:"count"`
	First string `json:"first"`
}

type jsonCheck struct {
	Name       string   `json:"name"`
	Passes     int      `json:"passes"`
	Fails      int      `json:"fails"`
	Rate       *float64 `json:"rate"`
	FirstError string   `json:"firstError"`
}

type jsonThreshold struct {
	Metric      string   `json:"metric"`
	Expression  string   `json:"expression"`
	Passed      bool     `json:"passed"`
	NoData      bool     `json:"noData"`
	Observed    *float64 `json:"observed"`
	Unit        string   `json:"unit"`
	Approximate bool     `json:"approximate"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func ptr[T any](v T) *T { return &v }

func fraction(part, whole int) *float64 {
	if whole == 0 {
		return nil
	}
	return ptr(float64(part) / float64(whole))
}

// JSON writes r as an indented JSON summary (schema version 1). version
// is the LoadTool version recorded in the document.
func JSON(w io.Writer, r Result, version string) error {
	doc := buildJSON(r, version)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Keep "p(95)<500" readable; the file is not embedded in HTML.
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

func buildJSON(r Result, version string) jsonSummary {
	s := r.Summary
	status := "completed"
	if r.Interrupted {
		status = "interrupted"
	}
	startedAt := ""
	if !r.Started.IsZero() {
		startedAt = r.Started.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}
	perSecond := func(n int) float64 {
		if r.Elapsed <= 0 {
			return 0
		}
		return float64(n) / r.Elapsed.Seconds()
	}

	var checkPasses, checkTotal int
	checks := make([]jsonCheck, 0, len(s.Checks))
	for _, c := range s.Checks {
		checkPasses += c.Passes
		checkTotal += c.Passes + c.Fails
		checks = append(checks, jsonCheck{Name: c.Name, Passes: c.Passes, Fails: c.Fails,
			Rate: fraction(c.Passes, c.Passes+c.Fails), FirstError: c.FirstError})
	}

	doc := jsonSummary{
		SchemaVersion: SchemaVersion,
		Tool:          jsonTool{Name: "loadtool", Version: version},
		Script:        r.Script,
		Status:        status,
		StartedAt:     startedAt,
		ElapsedMs:     ms(r.Elapsed),
		TeardownError: r.TeardownError,
		Config:        jsonConfig{VUs: r.VUs, DurationMs: ms(r.Duration), Scenarios: make([]jsonScenario, 0, len(r.Scenarios))},
		Metrics: jsonMetrics{
			HTTPReqs:          jsonCounter{Count: s.Requests, Rate: perSecond(s.Requests)},
			HTTPReqFailed:     jsonFailed{Rate: fraction(s.Failures, s.Requests), Failed: s.Failures, Count: s.Requests},
			Iterations:        jsonCounter{Count: s.Iterations, Rate: perSecond(s.Iterations)},
			DroppedIterations: jsonCounter{Count: s.DroppedIterations, Rate: perSecond(s.DroppedIterations)},
			Checks:            jsonRate{Rate: fraction(checkPasses, checkTotal), Passes: checkPasses, Fails: checkTotal - checkPasses, Count: checkTotal},
			ScriptErrors:      jsonScriptErr{Count: s.ScriptErrors, First: s.FirstScriptError},
		},
		Checks:     checks,
		Thresholds: make([]jsonThreshold, 0, len(r.Thresholds)),
		Series:     make([]jsonPoint, 0, len(r.Series)),
	}
	for _, p := range r.Series {
		jp := jsonPoint{AtMs: ms(p.At), VUs: p.VUs, HTTPReqs: p.Requests, HTTPReqFailed: p.Failed}
		if p.Requests > 0 {
			jp.Duration = &jsonPtDur{Avg: ms(p.Mean), P50: ms(p.P50), P95: ms(p.P95), P99: ms(p.P99)}
		}
		doc.Series = append(doc.Series, jp)
	}
	if s.Sent > 0 {
		doc.Metrics.HTTPReqDuration = &jsonTrend{Count: s.Sent,
			Min: ptr(ms(s.Min)), Avg: ptr(ms(s.Mean)), Max: ptr(ms(s.Max)),
			P50: ms(s.P50), P90: ms(s.P90), P95: ms(s.P95), P99: ms(s.P99)}
	}
	if s.Successes > 0 {
		doc.Metrics.SuccessDuration = &jsonTrend{Count: s.Successes,
			P50: ms(s.SuccessP50), P90: ms(s.SuccessP90), P95: ms(s.SuccessP95), P99: ms(s.SuccessP99)}
	}
	for _, sc := range r.Scenarios {
		doc.Config.Scenarios = append(doc.Config.Scenarios, scenarioJSON(sc))
	}
	for _, t := range r.Thresholds {
		jt := jsonThreshold{Metric: t.Metric, Expression: t.Expr, Passed: t.Passed, NoData: t.NoData,
			Unit: unitName(t.Unit), Approximate: t.Approximate}
		if !t.NoData {
			jt.Observed = ptr(t.Observed)
		}
		doc.Thresholds = append(doc.Thresholds, jt)
	}
	return doc
}

func scenarioJSON(sc config.Scenario) jsonScenario {
	j := jsonScenario{Name: sc.Name, Executor: sc.Executor, Exec: sc.Exec,
		StartTimeMs: ms(sc.StartTime), GracefulStopMs: ms(sc.GracefulStop)}
	switch sc.Executor {
	case config.ConstantVUs:
		j.VUs, j.DurationMs = ptr(sc.VUs), ptr(ms(sc.Duration))
	case config.RampingVUs:
		j.StartVUs, j.GracefulRampDownMs = ptr(sc.StartVUs), ptr(ms(sc.GracefulRampDown))
		for _, st := range sc.Stages {
			j.Stages = append(j.Stages, jsonStage{DurationMs: ms(st.Duration), Target: st.Target})
		}
	case config.ConstantArrivalRate:
		j.Rate, j.TimeUnitMs = ptr(sc.Rate), ptr(ms(sc.TimeUnit))
		j.DurationMs, j.PreAllocatedVUs = ptr(ms(sc.Duration)), ptr(sc.PreAllocatedVUs)
	}
	return j
}

func unitName(u thresholds.Unit) string {
	switch u {
	case thresholds.Milliseconds:
		return "ms"
	case thresholds.Fraction:
		return "fraction"
	case thresholds.PerSecond:
		return "per-second"
	default:
		return "count"
	}
}

// WriteJSONFile writes the JSON summary to path atomically: to a temporary
// file in the same directory, then renamed, so a reader never sees a
// partial file and a failed write leaves an existing file untouched.
func WriteJSONFile(path string, r Result, version string) error {
	return writeAtomic(path, ".loadtool-summary-*.json", func(w io.Writer) error { return JSON(w, r, version) })
}
