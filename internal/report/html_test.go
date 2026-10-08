package report

import (
	"bytes"
	"fmt"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

func renderHTML(t *testing.T, r Result) string {
	t.Helper()
	var b bytes.Buffer
	if err := HTML(&b, r, "v0.0.0-test"); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// The report must open offline and run nothing: no scripts, no external
// resources, for every golden case.
func TestHTMLIsSelfContained(t *testing.T) {
	external := regexp.MustCompile(`(?i)<script|<link|<iframe|<img|\bsrc=|\bhref=|url\(|@import|https?://`)
	cases := maps.Clone(goldenCases)
	cases["example"] = exampleResult()
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			out := renderHTML(t, r)
			if m := external.FindString(out); m != "" {
				t.Errorf("report contains %q", m)
			}
			for _, want := range []string{"<!doctype html>", "<title>", "</html>"} {
				if !strings.Contains(out, want) {
					t.Errorf("report misses %q", want)
				}
			}
		})
	}
}

// Text from the script (check names, errors) is escaped.
func TestHTMLEscapesScriptText(t *testing.T) {
	r := goldenCases["completed"]
	r.Summary.Checks = []metrics.CheckResult{{Name: `<script>alert("x")</script>`, Passes: 1, FirstError: `<b>bold</b>`}}
	r.Summary.FirstScriptError = `<img src=x onerror=alert(1)>`
	out := renderHTML(t, r)
	for _, raw := range []string{`<script>alert`, `<b>bold</b>`, `<img src=x`} {
		if strings.Contains(out, raw) {
			t.Errorf("report contains unescaped %q", raw)
		}
	}
	if !strings.Contains(out, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;") {
		t.Error("escaped check name not found")
	}
}

// The badge, exit code and reasons come from Verdict, the same decision
// as the CLI exit code and the JSON outcome.
func TestHTMLOutcome(t *testing.T) {
	teardown := goldenCases["thresholds"]
	teardown.TeardownError = "teardown: Error: cleanup failed"
	tests := []struct {
		name  string
		r     Result
		badge string
	}{
		{"completed", goldenCases["completed"], `<span class="badge ok">passed</span>`},
		{"thresholds", goldenCases["thresholds"], `<span class="badge fail">failed</span>`},
		{"teardown", teardown, `<span class="badge fail">failed</span>`},
		{"interrupted", goldenCases["interrupted-none-sent"], `<span class="badge fail">interrupted</span>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderHTML(t, tt.r)
			v := Verdict(tt.r)
			if !strings.Contains(out, tt.badge) {
				t.Errorf("missing badge %s", tt.badge)
			}
			if want := fmt.Sprintf("exit code %d</p>", v.ExitCode); !strings.Contains(out, want) {
				t.Errorf("missing %q", want)
			}
			if got := strings.Count(out, "<li>"); got != len(v.Reasons) {
				t.Errorf("%d reasons shown, want %d", got, len(v.Reasons))
			}
			for _, reason := range v.Reasons {
				if !strings.Contains(out, "<li>"+template.HTMLEscapeString(reason)+"</li>") {
					t.Errorf("reason %q not shown", reason)
				}
			}
		})
	}
	if !strings.Contains(renderHTML(t, goldenCases["thresholds"]), "<li>threshold failed: http_req_failed rate&lt;0.01</li>") {
		t.Error("the failed threshold is not listed as a reason")
	}
	if !strings.Contains(renderHTML(t, goldenCases["interrupted-none-sent"]), "No requests were sent.") {
		t.Error("an interrupted run with no requests should say so")
	}
}

// The summary cards hold the required numbers.
func TestHTMLSummaryCards(t *testing.T) {
	out := renderHTML(t, goldenCases["thresholds"])
	card := func(label, value string) string {
		return `<div class="label">` + label + `</div><div class="value">` + value + `</div>`
	}
	for _, want := range []string{
		card("Requests", "3,000"),
		card("Throughput", "100.0"), // 3,000 requests in 30 s
		card("Error rate", "2.00%"),
		card("p95 latency", "13.00ms"),
		card("VUs", "10"),
		card("Thresholds", "4 / 6"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing card %s", want)
		}
	}
	if !strings.Contains(renderHTML(t, goldenCases["checks"]), card("Checks", "99.93%")) {
		t.Error("missing the checks card")
	}
}

// A second without requests is a gap in the latency lines, not a zero.
func TestChartGaps(t *testing.T) {
	out := renderHTML(t, goldenCases["scenarios"])
	// The golden series has requests at 1s, none at 2s, some at 2.5s: each
	// latency line is two segments, so it starts with "M" twice.
	latency := out[strings.Index(out, "Latency <span"):strings.Index(out, "Requests per second")]
	paths := regexp.MustCompile(`<path d="([^"]+)"`).FindAllStringSubmatch(latency, -1)
	if len(paths) != 3 {
		t.Fatalf("got %d latency lines, want 3 (p50, p95, p99)", len(paths))
	}
	for _, p := range paths {
		if n := strings.Count(p[1], "M"); n != 2 {
			t.Errorf("line %q has %d segments, want 2 (a gap at 2s)", p[1], n)
		}
	}
	if !strings.Contains(renderHTML(t, goldenCases["completed"]), "Over time") == (len(goldenCases["completed"].Series) > 0) {
		t.Error("charts shown without a series, or missing with one")
	}
}

// The error-rate chart shows failed requests as a percentage of the
// requests in each second, with a gap where there were none.
func TestErrorRateChart(t *testing.T) {
	out := renderHTML(t, goldenCases["scenarios"])
	i := strings.Index(out, "Error rate <span")
	if i < 0 {
		t.Fatal("no error-rate chart")
	}
	chart := out[i:]
	chart = chart[:strings.Index(chart, "</figure>")]
	paths := regexp.MustCompile(`<path d="([^"]+)"`).FindAllStringSubmatch(chart, -1)
	if len(paths) != 1 || strings.Count(paths[0][1], "M") != 2 {
		t.Fatalf("want one line in two segments (a gap at 2s), got %v", paths)
	}
	// 1 of 70 requests failed in the first second: 1.43 %, under the
	// 1.5 top tick; the 2.5s point has none failed, so sits on the axis.
	if !strings.Contains(chart, `>1.5</text>`) {
		t.Error("the y axis does not end at 1.5 %")
	}
}

// A long run stays a small file: an hour of per-second points is well
// under a megabyte.
func TestHTMLSizeForLongRun(t *testing.T) {
	r := exampleResult()
	r.Series = nil
	for i := 1; i <= 3600; i++ {
		r.Series = append(r.Series, metrics.Point{At: time.Duration(i) * time.Second, Requests: 4000 + i%97, Failed: i % 13,
			P50: time.Duration(11000+i%300) * time.Microsecond, P95: time.Duration(15000+i%700) * time.Microsecond,
			P99: time.Duration(20000+i%1500) * time.Microsecond, VUs: 50})
	}
	out := renderHTML(t, r)
	t.Logf("%d points: %d bytes", len(r.Series), len(out))
	if len(out) > 1<<20 {
		t.Errorf("report is %d bytes, want under 1 MiB", len(out))
	}
}

func TestNiceTicks(t *testing.T) {
	tests := []struct {
		max  float64
		want []float64
	}{
		{0, []float64{0, 1}},
		{9, []float64{0, 2, 4, 6, 8, 10}},
		{10, []float64{0, 2, 4, 6, 8, 10}},
		{13.5, []float64{0, 5, 10, 15}},
		{0.42, []float64{0, 0.1, 0.2, 0.30000000000000004, 0.4, 0.5}},
		{2500, []float64{0, 500, 1000, 1500, 2000, 2500}},
	}
	for _, tt := range tests {
		if got := niceTicks(tt.max); !slices.Equal(got, tt.want) {
			t.Errorf("niceTicks(%v) = %v, want %v", tt.max, got, tt.want)
		}
	}
	if got := tickLabel(0.30000000000000004); got != "0.3" {
		t.Errorf("tickLabel = %q, want 0.3", got)
	}
}

func TestHTMLMatchesGolden(t *testing.T) {
	out := renderHTML(t, goldenCases["scenarios"])
	if *update {
		if err := os.WriteFile(filepath.Join("testdata", "scenarios.html"), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Join("testdata", "scenarios.html"))
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("output differs from scenarios.html; regenerate it if the change is intended")
	}
}

// WriteHTMLFile writes atomically and leaves no temporary file.
func TestWriteHTMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	r := goldenCases["thresholds"]
	if err := WriteHTMLFile(path, r, "v"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only report.html", len(entries))
	}
	if err := WriteHTMLFile(filepath.Join(dir, "missing", "r.html"), r, "v"); err == nil {
		t.Error("want an error for a missing directory")
	}
}

// On Windows, replacing a file another process holds open fails; the
// write retries, so a reader that lets go within the retry window does
// not fail the run.
func TestWriteRetriesWhileTargetIsOpen(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rename over an open file only fails on Windows")
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path) // held without delete sharing, like a scanner
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(200*time.Millisecond, func() { f.Close() })
	if err := WriteHTMLFile(path, goldenCases["completed"], "v"); err != nil {
		t.Fatalf("WriteHTMLFile = %v, want success once the file is released", err)
	}
	if b, _ := os.ReadFile(path); !strings.HasPrefix(string(b), "<!doctype html>") {
		t.Error("report was not replaced")
	}
}

// Used protocol metric families get a table; unused ones and runs without
// families get none.
func TestHTMLFamilies(t *testing.T) {
	out := renderHTML(t, goldenCases["families"])
	for _, want := range []string{
		"<h2>Protocol metrics</h2>",
		"<td><code>ws_connecting</code></td><td>trend</td><td>120 samples, 2 failed  avg=1.50ms p50=1.20ms p95=3.00ms p99=8.00ms max=9.00ms</td>",
		"<td><code>ws_msgs_sent</code></td><td>counter</td><td>4,000 (400.0/s)</td>",
		"<td><code>ws_session_failed</code></td><td>rate</td><td>1.67% (2 of 120)</td>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report misses %q", want)
		}
	}
	if strings.Contains(out, "grpc_reqs") {
		t.Error("the unused family is shown")
	}
	if strings.Contains(renderHTML(t, goldenCases["completed"]), "Protocol metrics") {
		t.Error("a run without families shows a protocol metrics table")
	}
}
