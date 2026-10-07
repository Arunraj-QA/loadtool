package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return executeContext(t, context.Background(), args...)
}

func executeContext(t *testing.T, ctx context.Context, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := NewRootCmd(&out, &errOut)
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

// scriptFile writes src to a test.ts file and returns its path.
func scriptFile(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.ts")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// getScript returns a script whose iteration sends one GET to url.
func getScript(t *testing.T, url string) string {
	t.Helper()
	return scriptFile(t, `import http from "loadtool/http";
export default function (): void { http.get("`+url+`"); }`)
}

func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRootCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{name: "help", args: []string{"--help"}, wantOutput: "run"},
		{name: "version", args: []string{"--version"}, wantOutput: Version},
		{name: "run help", args: []string{"run", "--help"}, wantOutput: "--vus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := execute(t, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, tt.wantOutput) {
				t.Errorf("output %q does not contain %q", out, tt.wantOutput)
			}
		})
	}
}

func TestRunSuccessfulRequests(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	out, _, err := execute(t, "run", getScript(t, srv.URL), "--vus", "3", "--duration", "150ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"VUs:         3", "Status:      completed", "Errors:      0 (0.00%)", "Script errs: 0", "p99", "Latency (successful requests):"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Requests:    0 ") {
		t.Errorf("expected requests to be sent:\n%s", out)
	}
}

func TestRunFailedRequests(t *testing.T) {
	srv := statusServer(t, http.StatusInternalServerError)
	out, _, err := execute(t, "run", getScript(t, srv.URL), "--vus", "2", "--duration", "100ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Success:     0\n", "(100.00%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestRunScriptErrorsDoNotStopTest(t *testing.T) {
	path := scriptFile(t, `export default function () { throw new Error("kaboom"); }`)
	out, _, err := execute(t, "run", path, "--vus", "2", "--duration", "100ms")
	if err != nil {
		t.Fatalf("script errors must not fail the run: %v", err)
	}
	for _, want := range []string{"Status:      completed", "kaboom", "Latency:     no requests were sent"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Script errs: 0\n") {
		t.Errorf("expected script errors to be counted:\n%s", out)
	}
}

func TestRunInterrupted(t *testing.T) {
	// Ctrl+C on the first request, so the load phase is surely running
	// (a timer could fire during start-up on a busy machine).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { cancel() }))
	t.Cleanup(srv.Close)

	start := time.Now()
	out, _, err := executeContext(t, ctx, "run", getScript(t, srv.URL), "--vus", "2", "--duration", "1h")
	if time.Since(start) > 10*time.Second {
		t.Fatal("run did not stop on cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !strings.Contains(out, "interrupted") {
		t.Errorf("expected partial summary, got:\n%s", out)
	}
}

func TestRunScriptLoadErrors(t *testing.T) {
	tests := []struct {
		name, src, wantErr string
	}{
		{"syntax error", "export default function ( {", "load script"},
		{"no default export", "export const x = 1;", "must export a default function"},
		{"init error", `throw new Error("init boom"); export default function () {}`, "init boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := execute(t, "run", scriptFile(t, tt.src), "--duration", "1h")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if out != "" {
				t.Errorf("no summary expected when the script cannot load, got:\n%s", out)
			}
		})
	}
}

func TestRunValidation(t *testing.T) {
	script := scriptFile(t, "export default function () {}")
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"missing script arg", []string{"run"}, "accepts 1 arg"},
		{"too many scripts", []string{"run", "a.ts", "b.ts"}, "accepts 1 arg"},
		{"zero vus", []string{"run", script, "--vus", "0"}, "vus must be at least 1"},
		{"bad duration", []string{"run", script, "--duration", "soon"}, "invalid argument"},
		{"script not found", []string{"run", filepath.Join(t.TempDir(), "nope.ts")}, "load script"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := execute(t, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, _, err := execute(t, "bogus"); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

// TestRunInterruptedDuringStartup covers Ctrl+C while VUs start: top-level
// script code that never finishes must not hang the command.
func TestRunInterruptedDuringStartup(t *testing.T) {
	path := scriptFile(t, "for (;;) {}\nexport default function () {}")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() {
		_, _, err := executeContext(t, ctx, "run", path, "--vus", "2", "--duration", "1h")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run hung in script start-up after cancellation")
	}
}

// TestRunGracefulStopCountsInFlightRequests: requests still running when
// --duration ends are completed and counted, not silently dropped.
func TestRunGracefulStopCountsInFlightRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	script := getScript(t, srv.URL)

	out, _, err := execute(t, "run", script, "--vus", "2", "--duration", "100ms", "--graceful-stop", "2s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Requests:    2 ") {
		t.Errorf("want the 2 in-flight requests counted:\n%s", out)
	}

	out, _, err = execute(t, "run", script, "--vus", "2", "--duration", "100ms", "--graceful-stop", "0s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Requests:    0 ") {
		t.Errorf("with --graceful-stop 0, in-flight requests are cancelled:\n%s", out)
	}
}

// TestRunUsesScriptOptions checks precedence end to end (ADR-006):
// script options < LOADTOOL_* environment variables < typed CLI flags.
func TestRunUsesScriptOptions(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	path := scriptFile(t, `import http from "loadtool/http";
export const options = { vus: 3, duration: "150ms", tags: {} };
export default function (): void { http.get("`+srv.URL+`"); }`)

	out, stderr, err := execute(t, "run", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "VUs:         3") || !strings.Contains(out, "Duration:    150ms") {
		t.Errorf("script options not applied:\n%s", out)
	}
	if !strings.Contains(stderr, `script option "tags" is not supported yet`) {
		t.Errorf("want a warning for the unsupported option, stderr: %q", stderr)
	}

	t.Setenv("LOADTOOL_VUS", "2")
	out, _, err = execute(t, "run", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "VUs:         2") {
		t.Errorf("LOADTOOL_VUS should override the script:\n%s", out)
	}

	out, _, err = execute(t, "run", path, "--vus", "4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "VUs:         4") || !strings.Contains(out, "Duration:    150ms") {
		t.Errorf("--vus should override env and script, duration still from the script:\n%s", out)
	}
}

func TestRunRejectsInvalidScriptOptions(t *testing.T) {
	path := scriptFile(t, `export const options = { vus: 0 }; export default function () {}`)
	_, _, err := execute(t, "run", path)
	if err == nil || !strings.Contains(err.Error(), "(from script options)") {
		t.Fatalf("error = %v, want it to name the script options as the source", err)
	}
}

func TestScriptEnv(t *testing.T) {
	environ := []string{"PATH=/bin", `=C:=C:\work`, "EMPTY=", "TARGET=http://old"}
	env, err := scriptEnv(environ, []string{"TARGET=http://new", "EXTRA=a=b"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PATH": "/bin", "EMPTY": "", "TARGET": "http://new", "EXTRA": "a=b"}
	if fmt.Sprint(env) != fmt.Sprint(want) {
		t.Errorf("scriptEnv = %v, want %v (Windows' hidden =C: entries are skipped, --env wins)", env, want)
	}
	for _, bad := range []string{"NOVALUE", "=value"} {
		if _, err := scriptEnv(nil, []string{bad}); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
			t.Errorf("scriptEnv(--env %q) error = %v, want a KEY=VALUE error", bad, err)
		}
	}
}

func TestRunEnvFlag(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	path := scriptFile(t, `import http from "loadtool/http";
export default function () {
	if (!__ENV.TARGET) throw new Error("TARGET not set");
	http.get(__ENV.TARGET);
}`)
	out, _, err := execute(t, "run", path, "--duration", "100ms", "-e", "TARGET="+srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Script errs: 0") || strings.Contains(out, "Requests:    0 ") {
		t.Errorf("want requests to __ENV.TARGET and no script errors:\n%s", out)
	}
	if _, _, err := execute(t, "run", path, "-e", "BROKEN"); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Errorf("error = %v, want a KEY=VALUE error for a malformed --env", err)
	}
}

func TestRunConsoleGoesToStderr(t *testing.T) {
	path := scriptFile(t, `console.log("init");
export default function () { if (__ITER === 0 && __VU === 1) console.warn("first iteration"); }`)
	out, stderr, err := execute(t, "run", path, "--duration", "50ms")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INFO  [VU 0] init", "INFO  [VU 1] init", "WARN  [VU 1] first iteration"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(out, "first iteration") {
		t.Errorf("console output must not go to stdout:\n%s", out)
	}
}

// A failed teardown fails the command, but only after the full summary was
// printed: the load phase's result is never hidden.
func TestRunTeardownFailure(t *testing.T) {
	path := scriptFile(t, `export default function () {}
export function teardown() { throw new Error("cleanup failed"); }`)
	out, _, err := execute(t, "run", path, "--duration", "50ms")
	if err == nil || !strings.Contains(err.Error(), "teardown: Error: cleanup failed") {
		t.Fatalf("error = %v, want the teardown error", err)
	}
	for _, want := range []string{"Status:      completed", "Teardown:    failed (teardown: Error: cleanup failed", "Requests:"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

// A failed setup fails the command before any load: no summary is printed.
func TestRunSetupFailure(t *testing.T) {
	path := scriptFile(t, `export function setup() { throw new Error("no credentials"); }
export default function () {}`)
	out, _, err := execute(t, "run", path, "--duration", "50ms")
	if err == nil || !strings.Contains(err.Error(), "setup: Error: no credentials") {
		t.Fatalf("error = %v, want the setup error", err)
	}
	if strings.Contains(out, "LoadTool summary") {
		t.Errorf("summary printed after a failed setup:\n%s", out)
	}
}

func TestRunThresholds(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	run := func(t *testing.T, thresholds string) (string, error) {
		path := scriptFile(t, `import http from "loadtool/http";
export const options = { thresholds: `+thresholds+` };
export default function () { http.get("`+srv.URL+`"); }`)
		out, _, err := execute(t, "run", path, "--duration", "100ms")
		return out, err
	}

	t.Run("pass", func(t *testing.T) {
		out, err := run(t, `{ http_req_failed: ["rate<0.01"], http_reqs: ["count>0"] }`)
		if err != nil || ExitCode(err) != 0 {
			t.Fatalf("error = %v, want success", err)
		}
		if !strings.Contains(out, "Thresholds:  2 of 2 passed") {
			t.Errorf("summary:\n%s", out)
		}
	})
	t.Run("fail", func(t *testing.T) {
		out, err := run(t, `{ http_req_failed: ["rate<0.01"], http_reqs: ["count<0"], checks: ["rate>0.9"] }`)
		if ExitCode(err) != ExitThresholdsFailed {
			t.Fatalf("exit code = %d (error %v), want %d", ExitCode(err), err, ExitThresholdsFailed)
		}
		if want := "thresholds failed: checks rate>0.9, http_reqs count<0"; err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
		// The full summary is printed before the failure is reported.
		for _, want := range []string{"Thresholds:  1 of 3 passed", "✗ checks", "no data", "Latency (all requests sent"} {
			if !strings.Contains(out, want) {
				t.Errorf("summary missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("invalid expression fails before load", func(t *testing.T) {
		out, err := run(t, `{ http_req_duration: ["p95<500"] }`)
		if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), `options.thresholds.http_req_duration: "p95<500"`) {
			t.Fatalf("error = %v (exit %d), want the parse error with exit 1", err, ExitCode(err))
		}
		if strings.Contains(out, "LoadTool summary") {
			t.Errorf("a summary was printed, so load ran:\n%s", out)
		}
	})
}

// An interrupt wins over failed thresholds: exit 1, not 99, because the
// metrics are partial.
func TestRunInterruptedWithFailedThresholds(t *testing.T) {
	// Ctrl+C on the first request, so the load phase is surely running.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { cancel() }))
	t.Cleanup(srv.Close)
	path := scriptFile(t, `import http from "loadtool/http";
export const options = { thresholds: { http_reqs: ["count<0"] } };
export default function () { http.get("`+srv.URL+`"); }`)
	_, _, err := executeContext(t, ctx, "run", path, "--duration", "1m")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "test interrupted") {
		t.Fatalf("error = %v (exit %d), want interrupted with exit 1", err, ExitCode(err))
	}
}

func TestExitCode(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", &ExitError{Code: 99, Err: errors.New("x")})
	for _, tt := range []struct {
		err  error
		want int
	}{{nil, 0}, {errors.New("boom"), 1}, {&ExitError{Code: 99, Err: errors.New("x")}, 99}, {wrapped, 99}} {
		if got := ExitCode(tt.err); got != tt.want {
			t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func readSummaryJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	return doc
}

func TestRunSummaryJSON(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	dir := t.TempDir()
	out := filepath.Join(dir, "summary.json")
	path := scriptFile(t, `import http from "loadtool/http";
import { check } from "loadtool";
export const options = { thresholds: { http_reqs: ["count>0"] } };
export default function () { check(http.get("`+srv.URL+`"), { ok: (r) => r.status === 200 }); }`)
	stdout, _, err := execute(t, "run", path, "--vus", "2", "--duration", "100ms", "--summary-json", out)
	if err != nil {
		t.Fatal(err)
	}
	doc := readSummaryJSON(t, out)
	metrics := doc["metrics"].(map[string]any)
	reqs := metrics["http_reqs"].(map[string]any)["count"].(float64)
	if doc["schemaVersion"] != 1.0 || doc["status"] != "completed" || reqs == 0 {
		t.Errorf("schemaVersion=%v status=%v requests=%v", doc["schemaVersion"], doc["status"], reqs)
	}
	// The same numbers as the console summary.
	if want := "Requests:    " + formatCountForTest(int(reqs)); !strings.Contains(stdout, want) {
		t.Errorf("console does not show %q:\n%s", want, stdout)
	}
	if th := doc["thresholds"].([]any); len(th) != 1 || th[0].(map[string]any)["passed"] != true {
		t.Errorf("thresholds = %v", th)
	}
	if checks := doc["checks"].([]any); len(checks) != 1 || checks[0].(map[string]any)["name"] != "ok" {
		t.Errorf("checks = %v", checks)
	}
	// Written atomically: no temporary file is left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only summary.json", len(entries))
	}
}

// formatCountForTest mirrors the console's thousands separators.
func formatCountForTest(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Failed thresholds still write the summary (exit 99); a summary that
// cannot be written fails the run with exit 1, after the console summary.
func TestRunSummaryJSONWithFailures(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	path := scriptFile(t, `import http from "loadtool/http";
export const options = { thresholds: { http_reqs: ["count<0"] } };
export default function () { http.get("`+srv.URL+`"); }`)

	out := filepath.Join(t.TempDir(), "summary.json")
	_, _, err := execute(t, "run", path, "--duration", "50ms", "--summary-json", out)
	if ExitCode(err) != ExitThresholdsFailed {
		t.Fatalf("exit code %d (%v), want %d", ExitCode(err), err, ExitThresholdsFailed)
	}
	if th := readSummaryJSON(t, out)["thresholds"].([]any); th[0].(map[string]any)["passed"] != false {
		t.Errorf("thresholds = %v", th)
	}

	missing := filepath.Join(t.TempDir(), "no-such-dir", "summary.json")
	stdout, _, err := execute(t, "run", path, "--duration", "50ms", "--summary-json", missing)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "--summary-json") {
		t.Fatalf("exit code %d (%v), want 1 with the write error", ExitCode(err), err)
	}
	if !strings.Contains(stdout, "LoadTool summary") {
		t.Errorf("console summary missing:\n%s", stdout)
	}
}

// No result, no file: a run that cannot start writes nothing.
func TestRunSummaryJSONNotWrittenWithoutResult(t *testing.T) {
	out := filepath.Join(t.TempDir(), "summary.json")
	path := scriptFile(t, `export const x = 1;`)
	if _, _, err := execute(t, "run", path, "--summary-json", out); err == nil {
		t.Fatal("want a start-up error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("summary file exists after a failed start (stat error %v)", err)
	}
}

func TestRunReportHTML(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.html")
	path := scriptFile(t, `import http from "loadtool/http";
export const options = { thresholds: { http_reqs: ["count<0"] } };
export default function () { http.get("`+srv.URL+`"); }`)
	_, _, err := execute(t, "run", path, "--duration", "100ms", "--report-html", out, "--summary-json", filepath.Join(dir, "s.json"))
	// Written even though thresholds failed (exit 99).
	if ExitCode(err) != ExitThresholdsFailed {
		t.Fatalf("exit code %d (%v)", ExitCode(err), err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{"<!doctype html>", `<span class="badge fail">failed</span>`, "count&lt;0", "Latency"} {
		if !strings.Contains(html, want) {
			t.Errorf("report misses %q", want)
		}
	}
	if _, _, err := execute(t, "run", path, "--duration", "50ms", "--report-html", filepath.Join(dir, "missing", "r.html")); ExitCode(err) != 1 || !strings.Contains(err.Error(), "--report-html") {
		t.Errorf("unwritable report: exit %d (%v), want 1", ExitCode(err), err)
	}
}

// --out json writes only JSON to stdout (the console summary goes to
// stderr), and its outcome matches the process exit code.
func TestRunOutJSONStdout(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	for _, tt := range []struct {
		name, thresholds string
		wantCode         int
	}{
		{"passed", `{ http_reqs: ["count>0"] }`, 0},
		{"threshold failed", `{ http_reqs: ["count<0"] }`, ExitThresholdsFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := scriptFile(t, `import http from "loadtool/http";
export const options = { thresholds: `+tt.thresholds+` };
export default function () { http.get("`+srv.URL+`"); }`)
			stdout, stderr, err := execute(t, "run", path, "--duration", "100ms", "--out", "json")
			if got := ExitCode(err); got != tt.wantCode {
				t.Fatalf("exit code %d (%v), want %d", got, err, tt.wantCode)
			}
			var doc struct {
				SchemaVersion int `json:"schemaVersion"`
				Outcome       struct {
					Passed   bool     `json:"passed"`
					ExitCode int      `json:"exitCode"`
					Reasons  []string `json:"reasons"`
				} `json:"outcome"`
			}
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
			}
			if doc.SchemaVersion != 1 || doc.Outcome.ExitCode != tt.wantCode || doc.Outcome.Passed != (tt.wantCode == 0) {
				t.Errorf("outcome %+v, want exit code %d", doc.Outcome, tt.wantCode)
			}
			if !strings.Contains(stderr, "LoadTool summary") || strings.Contains(stdout, "LoadTool summary") {
				t.Errorf("the console summary must go to stderr with --out json")
			}
		})
	}
}

func TestOutFlag(t *testing.T) {
	dir := t.TempDir()
	srv := statusServer(t, http.StatusOK)
	path := scriptFile(t, `import http from "loadtool/http";
export default function () { http.get("`+srv.URL+`"); }`)
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	// --out json=<file> and the --summary-json alias, both written.
	stdout, _, err := execute(t, "run", path, "--duration", "50ms", "--out", "json="+a, "--summary-json", b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "LoadTool summary") {
		t.Error("without --out json the console summary stays on stdout")
	}
	for _, f := range []string{a, b} {
		if readSummaryJSON(t, f)["schemaVersion"] != 1.0 {
			t.Errorf("%s is not a summary", f)
		}
	}
	for _, bad := range []string{"csv", "json=", "influxdb=http://x"} {
		if _, _, err := execute(t, "run", path, "--out", bad); err == nil || !strings.Contains(err.Error(), "supported outputs are json") {
			t.Errorf("--out %s: error = %v", bad, err)
		}
	}
}
