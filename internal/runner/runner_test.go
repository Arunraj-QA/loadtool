package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

func scriptFile(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.ts")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	return srv
}

func getScript(t *testing.T, url, extra string) string {
	t.Helper()
	return scriptFile(t, extra+`import http from "loadtool/http";
export default function (): void { http.get("`+url+`"); }`)
}

func intp(n int) *int                     { return &n }
func durp(d time.Duration) *time.Duration { return &d }

func TestRunReturnsResult(t *testing.T) {
	srv := okServer(t)
	path := getScript(t, srv.URL, "")
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(2), Duration: durp(150 * time.Millisecond)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Script != path || res.VUs != 2 || res.Duration != 150*time.Millisecond || res.GracefulStop != time.Second {
		t.Errorf("settings not carried into the result: %+v", res)
	}
	if res.Interrupted || res.Elapsed < 150*time.Millisecond {
		t.Errorf("Interrupted=%v Elapsed=%v, want a completed run of at least 150ms", res.Interrupted, res.Elapsed)
	}
	if res.Summary.Requests == 0 || res.Summary.Failures != 0 {
		t.Errorf("Summary = %+v, want successful requests", res.Summary)
	}
}

func TestRunResolvesFromScriptAndGetenv(t *testing.T) {
	srv := okServer(t)
	path := getScript(t, srv.URL, `export const options = { vus: 3, duration: "100ms" };`+"\n")
	getenv := func(k string) (string, bool) {
		if k == config.EnvVUs {
			return "4", true
		}
		return "", false
	}
	res, err := Run(context.Background(), Params{Config: config.Config{Script: path}, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	// VUs from the injected environment, duration from the script.
	if res.VUs != 4 || res.Duration != 100*time.Millisecond {
		t.Errorf("VUs=%d Duration=%v, want 4 (env) and 100ms (script)", res.VUs, res.Duration)
	}
}

func TestRunWarnsAboutUnsupportedOptions(t *testing.T) {
	srv := okServer(t)
	path := getScript(t, srv.URL, `export const options = { duration: "50ms", tags: {}, noVUConnectionReuse: true };`+"\n")
	var warnings []string
	_, err := Run(context.Background(), Params{
		Config: config.Config{Script: path},
		Warn:   func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], `"noVUConnectionReuse"`) || !strings.Contains(warnings[1], `"tags"`) {
		t.Fatalf("warnings = %q, want one per unsupported option, sorted", warnings)
	}

	// A nil Warn is allowed.
	if _, err := Run(context.Background(), Params{Config: config.Config{Script: path}}); err != nil {
		t.Fatal(err)
	}
}

func TestRunInterruptedIsNotAnError(t *testing.T) {
	// Ctrl+C on the third request: the load phase is surely running and
	// earlier requests have completed (the cancelled one is not counted).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		if seen.Add(1) == 3 {
			cancel()
		}
	}))
	t.Cleanup(srv.Close)
	path := getScript(t, srv.URL, "")

	res, err := Run(ctx, Params{
		Config:    config.Config{Script: path},
		Overrides: config.Overrides{Duration: durp(time.Hour)},
	})
	if err != nil {
		t.Fatalf("an interrupted run must return its partial result, got error %v", err)
	}
	if !res.Interrupted || res.Summary.Requests == 0 {
		t.Errorf("Interrupted=%v Requests=%d, want a partial result marked interrupted", res.Interrupted, res.Summary.Requests)
	}
}

func TestRunStartErrors(t *testing.T) {
	tests := []struct {
		name string
		p    Params
		want string
	}{
		{"no script", Params{}, "script path is required"},
		{"missing file", Params{Config: config.Config{Script: filepath.Join(t.TempDir(), "nope.ts")}}, "load script"},
		{"syntax error", Params{Config: config.Config{Script: scriptFile(t, "export default function ( {")}}, "load script"},
		{"invalid options", Params{Config: config.Config{Script: scriptFile(t, `export const options = { vus: 0 }; export default function () {}`)}}, "(from script options)"},
		{"top-level error", Params{Config: config.Config{Script: scriptFile(t, `throw new Error("broken init"); export default function () {}`)}}, "script init: Error: broken init"},
		{"no default export", Params{Config: config.Config{Script: scriptFile(t, `export const options = { vus: 1 };`)}}, "must export a default function"},
		{"negative graceful stop", Params{Config: config.Config{Script: scriptFile(t, "export default function () {}"), GracefulStop: -time.Second}}, "graceful-stop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Run(context.Background(), tt.p)
			if err == nil {
				t.Fatalf("want an error, got result %+v", res)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
			if !reflect.DeepEqual(res, report.Result{}) {
				t.Errorf("want no result on a start-up error, got %+v", res)
			}
		})
	}
}

// options.discardResponseBodies reaches the VUs, and warnings raised while
// the test runs reach Params.Warn.
func TestRunAppliesHTTPOptionsAndWarnings(t *testing.T) {
	srv := okServer(t)
	for _, tt := range []struct {
		discard  string
		wantBody string
	}{
		{"false", "string"},
		{"true", "object"}, // typeof null
	} {
		t.Run("discard="+tt.discard, func(t *testing.T) {
			path := scriptFile(t, `import http from "loadtool/http";
export const options = { discardResponseBodies: `+tt.discard+` };
export default function (): void {
	const res = http.get("`+srv.URL+`", { timeout: "1s" });
	if (typeof res.body !== "`+tt.wantBody+`") throw new Error("typeof body is " + typeof res.body);
}`)
			var warnings []string
			res, err := Run(context.Background(), Params{
				Config:    config.Config{Script: path, GracefulStop: time.Second},
				Overrides: config.Overrides{VUs: intp(2), Duration: durp(50 * time.Millisecond)},
				Warn:      func(msg string) { warnings = append(warnings, msg) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.Summary.ScriptErrors != 0 {
				t.Fatalf("script error: %s", res.Summary.FirstScriptError)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], `"timeout" is not supported`) {
				t.Errorf("warnings = %q, want one about timeout", warnings)
			}
		})
	}
}
