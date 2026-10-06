package report

import (
	"bytes"
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
	for name, r := range goldenCases {
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

func TestHTMLStatusBadge(t *testing.T) {
	if out := renderHTML(t, goldenCases["completed"]); !strings.Contains(out, `<span class="badge ok">passed</span>`) {
		t.Error("a clean run should show passed")
	}
	if out := renderHTML(t, goldenCases["thresholds"]); !strings.Contains(out, `<span class="badge fail">failed</span>`) {
		t.Error("failed thresholds should show failed")
	}
	if out := renderHTML(t, goldenCases["interrupted-none-sent"]); !strings.Contains(out, `<span class="badge fail">interrupted</span>`) || !strings.Contains(out, "No requests were sent.") {
		t.Error("an interrupted run with no requests should say so")
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
