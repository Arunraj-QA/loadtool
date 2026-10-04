package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	srv := statusServer(t, http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

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
export const options = { vus: 3, duration: "150ms", thresholds: {} };
export default function (): void { http.get("`+srv.URL+`"); }`)

	out, stderr, err := execute(t, "run", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "VUs:         3") || !strings.Contains(out, "Duration:    150ms") {
		t.Errorf("script options not applied:\n%s", out)
	}
	if !strings.Contains(stderr, `script option "thresholds" is not supported yet`) {
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
