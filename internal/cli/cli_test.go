package cli

import (
	"bytes"
	"context"
	"errors"
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
	return scriptFile(t, `export default function (): void { http.get("`+url+`"); }`)
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
	for _, want := range []string{"VUs:         3", "Status:      completed", "Errors:      0 (0.00%)", "Script errs: 0", "p99"} {
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
	for _, want := range []string{"Status:      completed", "kaboom", "Latency:     no completed requests"} {
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

func TestFormatCount(t *testing.T) {
	tests := map[int]string{0: "0", 999: "999", 1000: "1,000", 125430: "125,430", 1234567: "1,234,567"}
	for n, want := range tests {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := map[time.Duration]string{
		450 * time.Microsecond:   "450.00µs",
		42500 * time.Microsecond: "42.50ms",
		1500 * time.Millisecond:  "1.50s",
	}
	for d, want := range tests {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
