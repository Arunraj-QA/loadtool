package report

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

// HTML writes r as a self-contained HTML report (ADR-012): one file with
// inline CSS and server-rendered SVG charts, no scripts and no external
// resources, so it opens offline and can be attached to a CI run.
func HTML(w io.Writer, r Result, version string) error {
	return htmlTemplate.Execute(w, buildHTML(r, version))
}

// WriteHTMLFile writes the HTML report to path atomically, like
// WriteJSONFile.
func WriteHTMLFile(path string, r Result, version string) error {
	return writeAtomic(path, ".loadtool-report-*.html", func(w io.Writer) error { return HTML(w, r, version) })
}

// writeAtomic writes to a temporary file in path's directory and renames
// it to path, so a reader never sees a partial file.
func writeAtomic(path, pattern string, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := write(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return replaceFile(tmp.Name(), path)
}

// replaceFile renames from to to. On Windows, replacing a file that
// another process has open fails ("Access is denied"), and virus
// scanners, search indexers and browsers commonly hold a freshly written
// report open for a moment; so there it retries for up to about a second
// before giving up.
func replaceFile(from, to string) error {
	err := os.Rename(from, to)
	for wait := 10 * time.Millisecond; err != nil && runtime.GOOS == "windows" && wait <= 640*time.Millisecond; wait *= 2 {
		time.Sleep(wait)
		err = os.Rename(from, to)
	}
	return err
}

type htmlData struct {
	Title, Script, Status, Version, Started string
	Interrupted, Failed                     bool
	TeardownError                           string
	Cards                                   []htmlCard
	Thresholds                              []htmlThreshold
	Checks                                  []htmlCheck
	Charts                                  []template.HTML
	Latency                                 []htmlRow
	Scenarios                               []string
	ScriptErrors, FirstScriptError          string
	Dropped                                 string
}

type htmlCard struct{ Label, Value, Note string }

type htmlThreshold struct {
	Passed                 bool
	Metric, Expr, Observed string
}

type htmlCheck struct {
	Passed                  bool
	Name, Passes, Rate, Err string
}

type htmlRow struct{ Name, All, OK string }

func buildHTML(r Result, version string) htmlData {
	s := r.Summary
	d := htmlData{
		Title:            "LoadTool report: " + filepath.Base(r.Script),
		Script:           r.Script,
		Status:           "completed",
		Version:          version,
		Interrupted:      r.Interrupted,
		TeardownError:    r.TeardownError,
		ScriptErrors:     formatCount(s.ScriptErrors),
		FirstScriptError: s.FirstScriptError,
	}
	if r.Interrupted {
		d.Status = "interrupted (partial results)"
	}
	if !r.Started.IsZero() {
		d.Started = r.Started.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	if s.DroppedIterations > 0 {
		d.Dropped = formatCount(s.DroppedIterations)
	}
	d.Failed = r.Interrupted || r.TeardownError != "" || thresholds.Failed(r.Thresholds)

	var rps float64
	if secs := r.Elapsed.Seconds(); secs > 0 {
		rps = float64(s.Requests) / secs
	}
	p95 := "—"
	if s.Sent > 0 {
		p95 = formatDuration(s.P95)
	}
	d.Cards = []htmlCard{
		{"Requests", formatCount(s.Requests), fmt.Sprintf("%.1f req/s", rps)},
		{"Errors", fmt.Sprintf("%.2f%%", s.ErrorRate*100), formatCount(s.Failures) + " failed"},
		{"p95 latency", p95, "all requests sent"},
		{"VUs", formatCount(r.VUs), "for " + r.Duration.String() + " (elapsed " + formatDuration(r.Elapsed) + ")"},
	}
	if len(s.Checks) > 0 {
		var passes, total int
		for _, c := range s.Checks {
			passes += c.Passes
			total += c.Passes + c.Fails
			d.Checks = append(d.Checks, htmlCheck{Passed: c.Fails == 0, Name: c.Name,
				Passes: formatCount(c.Passes) + " / " + formatCount(c.Passes+c.Fails),
				Rate:   percent(c.Passes, c.Passes+c.Fails), Err: c.FirstError})
		}
		d.Cards = append(d.Cards, htmlCard{"Checks", percent(passes, total), formatCount(passes) + " of " + formatCount(total) + " passed"})
	}
	if len(r.Thresholds) > 0 {
		passed := 0
		for _, t := range r.Thresholds {
			if t.Passed {
				passed++
			}
			d.Thresholds = append(d.Thresholds, htmlThreshold{Passed: t.Passed, Metric: t.Metric, Expr: t.Expr, Observed: observed(t)})
		}
		d.Cards = append(d.Cards, htmlCard{"Thresholds", fmt.Sprintf("%d / %d", passed, len(r.Thresholds)), "passed"})
	}
	if s.Sent > 0 {
		row := func(name string, all, ok time.Duration, hasOK bool) htmlRow {
			okText := "—"
			if hasOK && s.Successes > 0 {
				okText = formatDuration(ok)
			}
			return htmlRow{name, formatDuration(all), okText}
		}
		d.Latency = []htmlRow{
			row("min", s.Min, 0, false), row("mean", s.Mean, 0, false),
			row("p50", s.P50, s.SuccessP50, true), row("p90", s.P90, s.SuccessP90, true),
			row("p95", s.P95, s.SuccessP95, true), row("p99", s.P99, s.SuccessP99, true),
			row("max", s.Max, 0, false),
		}
	}
	if !isShorthand(r.Scenarios) {
		for _, sc := range r.Scenarios {
			d.Scenarios = append(d.Scenarios, sc.Describe())
		}
	}
	d.Charts = charts(r.Series)
	return d
}

// charts renders the time series as three SVG line charts; a run shorter
// than two points (about two seconds) gets none, since one point draws
// no line.
func charts(series []metrics.Point) []template.HTML {
	if len(series) < 2 {
		return nil
	}
	xs := make([]float64, len(series))
	p50, p95, p99 := make([]float64, len(series)), make([]float64, len(series)), make([]float64, len(series))
	reqs, failed, vus := make([]float64, len(series)), make([]float64, len(series)), make([]float64, len(series))
	prev := time.Duration(0)
	for i, p := range series {
		xs[i] = p.At.Seconds()
		secs := (p.At - prev).Seconds()
		prev = p.At
		if secs <= 0 {
			secs = 1
		}
		reqs[i], failed[i], vus[i] = float64(p.Requests)/secs, float64(p.Failed)/secs, float64(p.VUs)
		p50[i], p95[i], p99[i] = math.NaN(), math.NaN(), math.NaN() // a gap: no requests
		if p.Requests > 0 {
			p50[i], p95[i], p99[i] = ms(p.P50), ms(p.P95), ms(p.P99)
		}
	}
	return []template.HTML{
		lineChart("Latency", "ms", xs, []line{{"p50", "--c1", p50}, {"p95", "--c2", p95}, {"p99", "--c3", p99}}),
		lineChart("Requests per second", "req/s", xs, []line{{"requests", "--c1", reqs}, {"failed", "--bad", failed}}),
		lineChart("Active VUs", "VUs", xs, []line{{"VUs", "--c1", vus}}),
	}
}

type line struct {
	name, color string // color is a CSS variable
	ys          []float64
}

// Chart geometry, in SVG user units.
const (
	chartW, chartH         = 720, 240
	padL, padR, padT, padB = 56, 16, 16, 32
	plotW, plotH           = chartW - padL - padR, chartH - padT - padB
)

// lineChart draws lines over a shared x axis in seconds. NaN values are
// gaps. Text is fixed or numeric, so building the SVG as a string is
// safe; titles are escaped anyway.
func lineChart(title, unit string, xs []float64, lines []line) template.HTML {
	xMax := xs[len(xs)-1]
	yMax := 0.0
	for _, l := range lines {
		for _, y := range l.ys {
			if !math.IsNaN(y) {
				yMax = math.Max(yMax, y)
			}
		}
	}
	yTicks := niceTicks(yMax)
	yTop := yTicks[len(yTicks)-1]
	// The x axis ends where the data ends; ticks beyond it are dropped.
	xTop := xMax
	var xTicks []float64
	for _, t := range niceTicks(xMax) {
		if t <= xMax {
			xTicks = append(xTicks, t)
		}
	}
	px := func(x float64) float64 { return padL + x/xTop*plotW }
	py := func(y float64) float64 { return padT + plotH - y/yTop*plotH }

	var b strings.Builder
	esc := template.HTMLEscapeString
	fmt.Fprintf(&b, `<figure class="chart"><figcaption>%s <span class="unit">(%s)</span></figcaption>`, esc(title), esc(unit))
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s over time in seconds">`, chartW, chartH, esc(title))
	for _, t := range yTicks {
		y := py(t)
		fmt.Fprintf(&b, `<line class="grid" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>`, padL, chartW-padR, y, y)
		fmt.Fprintf(&b, `<text class="tick" x="%d" y="%.1f" text-anchor="end">%s</text>`, padL-6, y+4, tickLabel(t))
	}
	for _, t := range xTicks {
		x := px(t)
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%d" text-anchor="middle">%ss</text>`, x, chartH-10, tickLabel(t))
	}
	fmt.Fprintf(&b, `<line class="axis" x1="%d" x2="%d" y1="%d" y2="%d"/>`, padL, chartW-padR, padT+plotH, padT+plotH)
	for _, l := range lines {
		var path strings.Builder
		pen := false
		for i, y := range l.ys {
			if math.IsNaN(y) {
				pen = false
				continue
			}
			cmd := "L"
			if !pen {
				cmd = "M"
			}
			fmt.Fprintf(&path, "%s%.1f %.1f ", cmd, px(xs[i]), py(y))
			pen = true
		}
		if path.Len() > 0 {
			fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="var(%s)" stroke-width="2" stroke-linejoin="round"><title>%s</title></path>`,
				strings.TrimSpace(path.String()), l.color, esc(l.name))
		}
	}
	b.WriteString(`</svg><div class="legend">`)
	for _, l := range lines {
		fmt.Fprintf(&b, `<span><i style="background:var(%s)"></i>%s</span>`, l.color, esc(l.name))
	}
	b.WriteString(`</div></figure>`)
	return template.HTML(b.String())
}

// niceTicks returns 0 and up to about 5 evenly spaced round ticks covering
// max (1, 2 or 5 times a power of ten apart).
func niceTicks(max float64) []float64 {
	if max <= 0 || math.IsNaN(max) {
		return []float64{0, 1}
	}
	raw := max / 5
	pow := math.Pow(10, math.Floor(math.Log10(raw)))
	step := pow
	for _, m := range []float64{1, 2, 5, 10} {
		if m*pow >= raw {
			step = m * pow
			break
		}
	}
	var ticks []float64
	for t := 0.0; ; t += step {
		ticks = append(ticks, t)
		if t >= max {
			return ticks
		}
	}
}

func tickLabel(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}

var htmlTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="LoadTool {{.Version}}">
<title>{{.Title}}</title>
<style>
:root { --bg:#ffffff; --fg:#1b1f24; --muted:#5b6470; --card:#f4f6f8; --line:#d6dbe1;
  --c1:#2563eb; --c2:#d97706; --c3:#7c3aed; --good:#15803d; --bad:#dc2626; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#0f1318; --fg:#e6e9ed; --muted:#9aa4b0; --card:#1a2028; --line:#2c3440;
    --c1:#60a5fa; --c2:#fbbf24; --c3:#c4b5fd; --good:#4ade80; --bad:#f87171; }
}
* { box-sizing: border-box; }
body { margin: 0; padding: 24px 16px 48px; background: var(--bg); color: var(--fg);
  font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 1040px; margin: 0 auto; }
h1 { font-size: 1.5rem; margin: 0 0 4px; word-break: break-all; }
h2 { font-size: 1.1rem; margin: 32px 0 12px; }
.meta { color: var(--muted); margin: 0; }
.badge { display: inline-block; padding: 2px 10px; border-radius: 999px; font-weight: 600; font-size: .85rem; }
.badge.ok { background: color-mix(in srgb, var(--good) 15%, transparent); color: var(--good); }
.badge.fail { background: color-mix(in srgb, var(--bad) 15%, transparent); color: var(--bad); }
.alert { border-left: 4px solid var(--bad); background: var(--card); padding: 8px 12px; margin: 16px 0; }
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; margin-top: 20px; }
.card { background: var(--card); border-radius: 8px; padding: 12px 14px; }
.card .label { color: var(--muted); font-size: .85rem; }
.card .value { font-size: 1.5rem; font-weight: 650; font-variant-numeric: tabular-nums; }
.card .note { color: var(--muted); font-size: .8rem; }
table { border-collapse: collapse; width: 100%; font-variant-numeric: tabular-nums; }
th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
th { color: var(--muted); font-weight: 600; font-size: .85rem; }
th.num, td.num { text-align: right; }
.pass { color: var(--good); font-weight: 700; }
.fail { color: var(--bad); font-weight: 700; }
.err { color: var(--muted); font-size: .85rem; word-break: break-word; }
.scroll { overflow-x: auto; }
.charts { display: grid; gap: 16px; }
.chart { margin: 0; background: var(--card); border-radius: 8px; padding: 12px; }
.chart figcaption { font-weight: 600; margin-bottom: 4px; }
.chart .unit { color: var(--muted); font-weight: 400; }
.chart svg { width: 100%; height: auto; display: block; }
.chart .grid { stroke: var(--line); stroke-width: 1; }
.chart .axis { stroke: var(--muted); stroke-width: 1; }
.chart .tick { fill: var(--muted); font-size: 11px; }
.legend { display: flex; gap: 16px; flex-wrap: wrap; color: var(--muted); font-size: .85rem; }
.legend i { display: inline-block; width: 12px; height: 3px; margin-right: 6px; vertical-align: middle; }
footer { color: var(--muted); font-size: .8rem; margin-top: 40px; }
</style>
</head>
<body>
<main>
<h1>{{.Script}}</h1>
<p class="meta">
{{if .Failed}}<span class="badge fail">{{if .Interrupted}}interrupted{{else}}failed{{end}}</span>{{else}}<span class="badge ok">passed</span>{{end}}
{{if .Started}} · started {{.Started}}{{end}} · {{.Status}}</p>
{{if .TeardownError}}<p class="alert">Teardown failed: {{.TeardownError}}</p>{{end}}
{{if .Dropped}}<p class="alert">{{.Dropped}} iterations were dropped (no free VU): the target arrival rate was not reached. Raise preAllocatedVUs.</p>{{end}}

<section class="cards" aria-label="Summary">
{{range .Cards}}<div class="card"><div class="label">{{.Label}}</div><div class="value">{{.Value}}</div><div class="note">{{.Note}}</div></div>
{{end}}</section>

{{if .Thresholds}}<h2>Thresholds</h2>
<div class="scroll"><table>
<thead><tr><th scope="col">Result</th><th scope="col">Metric</th><th scope="col">Expression</th><th scope="col">Observed</th></tr></thead>
<tbody>{{range .Thresholds}}<tr><td>{{if .Passed}}<span class="pass">✓ pass</span>{{else}}<span class="fail">✗ fail</span>{{end}}</td><td>{{.Metric}}</td><td><code>{{.Expr}}</code></td><td>{{.Observed}}</td></tr>
{{end}}</tbody></table></div>{{end}}

{{if .Checks}}<h2>Checks</h2>
<div class="scroll"><table>
<thead><tr><th scope="col">Result</th><th scope="col">Check</th><th scope="col" class="num">Passed</th><th scope="col" class="num">Rate</th></tr></thead>
<tbody>{{range .Checks}}<tr><td>{{if .Passed}}<span class="pass">✓</span>{{else}}<span class="fail">✗</span>{{end}}</td><td>{{.Name}}{{if .Err}}<div class="err">first error: {{.Err}}</div>{{end}}</td><td class="num">{{.Passes}}</td><td class="num">{{.Rate}}</td></tr>
{{end}}</tbody></table></div>{{end}}

{{if .Charts}}<h2>Over time</h2>
<div class="charts">{{range .Charts}}{{.}}{{end}}</div>{{end}}

<h2>Latency</h2>
{{if .Latency}}<div class="scroll"><table>
<thead><tr><th scope="col"></th><th scope="col" class="num">All requests sent</th><th scope="col" class="num">Successful only</th></tr></thead>
<tbody>{{range .Latency}}<tr><th scope="row">{{.Name}}</th><td class="num">{{.All}}</td><td class="num">{{.OK}}</td></tr>
{{end}}</tbody></table></div>
<p class="meta">Percentiles are within ±0.78 %; min, mean and max are exact.</p>
{{else}}<p class="meta">No requests were sent.</p>{{end}}

<h2>Run</h2>
<div class="scroll"><table><tbody>
<tr><th scope="row">Script errors</th><td>{{.ScriptErrors}}{{if .FirstScriptError}}<div class="err">first: {{.FirstScriptError}}</div>{{end}}</td></tr>
{{range $i, $s := .Scenarios}}<tr><th scope="row">{{if eq $i 0}}Scenarios{{end}}</th><td>{{$s}}</td></tr>
{{end}}</tbody></table></div>

<footer>Generated by LoadTool {{.Version}}. Self-contained: no scripts or external resources.</footer>
</main>
</body>
</html>
`))
