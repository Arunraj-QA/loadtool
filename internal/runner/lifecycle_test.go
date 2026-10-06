package runner

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// eventServer logs every request's path, in order, so tests can check the
// lifecycle order. /work also records the Authorization header it got.
//
// cancelOn makes the server cancel a test's context when it first sees
// that path: tests interrupt a phase by the request that proves the phase
// is running, never by a timer, so they do not depend on how fast the
// machine (or the race detector) is.
type eventServer struct {
	*httptest.Server
	mu     sync.Mutex
	events []string
	auth   map[string]bool

	cancelOn   string
	cancel     context.CancelFunc
	cancelOnce sync.Once
}

func newEventServer(t *testing.T) *eventServer {
	t.Helper()
	s := &eventServer{auth: make(map[string]bool)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if n := len(s.events); n == 0 || s.events[n-1] != r.URL.Path {
			s.events = append(s.events, r.URL.Path) // collapse repeats of /work
		}
		if r.URL.Path == "/work" {
			s.auth[r.Header.Get("Authorization")] = true
		}
		cancel := s.cancel
		match := r.URL.Path == s.cancelOn
		s.mu.Unlock()
		if match && cancel != nil {
			s.cancelOnce.Do(cancel)
		}
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"token":"secret"}`))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// cancelWhenSeen returns a context that the server cancels when it first
// receives a request for path.
func (s *eventServer) cancelWhenSeen(t *testing.T, path string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.mu.Lock()
	s.cancelOn, s.cancel = path, cancel
	s.mu.Unlock()
	return ctx
}

func (s *eventServer) log() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.events, " ")
}

// lifecycleScript logs in during setup, sends the token from every VU and
// logs out in teardown. extra is inserted before the default function.
func lifecycleScript(t *testing.T, url, extra string) string {
	return scriptFile(t, `import http from "loadtool/http";
export function setup() {
	return { token: http.post("`+url+`/login", "{}").json().token };
}
`+extra+`
export default function (data: any) {
	http.get("`+url+`/work", { headers: { Authorization: data.token } });
}
export function teardown(data: any) {
	http.post("`+url+`/logout", data.token);
}`)
}

func runFor(ctx context.Context, path string, d time.Duration) (report.Result, error) {
	return Run(ctx, Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(3), Duration: durp(d)},
	})
}

func TestLifecycleOrderAndMetrics(t *testing.T) {
	srv := newEventServer(t)
	res, err := runFor(context.Background(), lifecycleScript(t, srv.URL, ""), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.log(); got != "/login /work /logout" {
		t.Errorf("server saw %q, want setup, then the load phase, then teardown", got)
	}
	srv.mu.Lock()
	auth := maps.Clone(srv.auth)
	srv.mu.Unlock()
	if len(auth) != 1 || !auth["secret"] {
		t.Errorf("work requests carried %v, want only the setup token", auth)
	}
	// Only the load phase's requests are counted.
	s := res.Summary
	if s.Requests == 0 || s.Failures != 0 || s.ScriptErrors != 0 {
		t.Errorf("Summary = %+v, want successful work requests only", s)
	}
	if res.TeardownError != "" || res.Interrupted {
		t.Errorf("TeardownError=%q Interrupted=%v", res.TeardownError, res.Interrupted)
	}
}

func TestSetupFailureStopsTheTest(t *testing.T) {
	srv := newEventServer(t)
	path := scriptFile(t, `import http from "loadtool/http";
export function setup() { throw new Error("no credentials"); }
export default function () { http.get("`+srv.URL+`/work"); }
export function teardown() { http.post("`+srv.URL+`/logout", ""); }`)
	_, err := runFor(context.Background(), path, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "setup: Error: no credentials") {
		t.Fatalf("error = %v, want the setup error", err)
	}
	if got := srv.log(); got != "" {
		t.Errorf("server saw %q, want nothing: no load phase and no teardown after a failed setup", got)
	}
}

func TestTeardownFailureKeepsTheResult(t *testing.T) {
	srv := newEventServer(t)
	path := scriptFile(t, `import http from "loadtool/http";
export default function () { http.get("`+srv.URL+`/work"); }
export function teardown() { throw new Error("cleanup failed"); }`)
	res, err := runFor(context.Background(), path, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Run error = %v; a teardown failure must not replace the result", err)
	}
	if !strings.Contains(res.TeardownError, "teardown: Error: cleanup failed") {
		t.Errorf("TeardownError = %q", res.TeardownError)
	}
	if res.Summary.Requests == 0 {
		t.Errorf("Summary = %+v, want the load phase's requests", res.Summary)
	}
}

// Teardown releases what setup created even when the VUs cannot start.
func TestTeardownRunsWhenVUStartupFails(t *testing.T) {
	srv := newEventServer(t)
	path := lifecycleScript(t, srv.URL, `if (__VU === 2) throw new Error("VU 2 cannot start");`)
	_, err := runFor(context.Background(), path, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "VU 2 cannot start") {
		t.Fatalf("error = %v, want the VU start-up error", err)
	}
	if got := srv.log(); got != "/login /logout" {
		t.Errorf("server saw %q, want setup then teardown and no load", got)
	}
}

// Ctrl+C during the load phase still runs teardown, and the result is
// marked interrupted.
func TestTeardownRunsAfterInterrupt(t *testing.T) {
	srv := newEventServer(t)
	ctx := srv.cancelWhenSeen(t, "/work") // Ctrl+C once the load phase runs
	res, err := runFor(ctx, lifecycleScript(t, srv.URL, ""), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interrupted || res.TeardownError != "" {
		t.Errorf("Interrupted=%v TeardownError=%q, want interrupted with a successful teardown", res.Interrupted, res.TeardownError)
	}
	// Requests the interrupt cancelled may still reach the server after
	// teardown's (cancellation is client-side), so the server's order
	// after the first /work is not checked; that teardown starts after
	// every VU has returned is guaranteed by engine.RunScenarios.
	got := srv.log()
	if !strings.HasPrefix(got, "/login /work") || !strings.Contains(got, "/logout") {
		t.Errorf("server saw %q, want setup, the load phase and then teardown despite the interrupt", got)
	}
}

// Ctrl+C during setup ends the test without a load phase or teardown.
func TestInterruptDuringSetup(t *testing.T) {
	srv := newEventServer(t)
	path := scriptFile(t, `import http from "loadtool/http";
import { sleep } from "loadtool";
export function setup() { http.get("`+srv.URL+`/setup-started"); sleep(60); }
export default function () { http.get("`+srv.URL+`/work"); }
export function teardown() { http.post("`+srv.URL+`/logout", ""); }`)
	ctx := srv.cancelWhenSeen(t, "/setup-started")
	start := time.Now()
	_, err := runFor(ctx, path, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "setup interrupted") {
		t.Fatalf("error = %v, want setup interrupted", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("Run took %v; setup did not stop promptly", time.Since(start))
	}
	if got := srv.log(); got != "/setup-started" {
		t.Errorf("server saw %q, want only the setup request: no load phase and no teardown", got)
	}
}

// A Ctrl+C during teardown (after a completed load phase) stops teardown
// and is reported as a teardown failure; the load phase is not interrupted.
func TestInterruptDuringTeardown(t *testing.T) {
	srv := newEventServer(t)
	path := scriptFile(t, `import http from "loadtool/http";
import { sleep } from "loadtool";
export default function () { http.get("`+srv.URL+`/work"); }
export function teardown() { http.get("`+srv.URL+`/teardown-started"); sleep(60); }`)
	// Ctrl+C once teardown runs, so the load phase has completed.
	ctx := srv.cancelWhenSeen(t, "/teardown-started")
	start := time.Now()
	res, err := runFor(ctx, path, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.Interrupted {
		t.Error("Interrupted = true, want false: the load phase completed")
	}
	if !strings.Contains(res.TeardownError, "teardown interrupted") {
		t.Errorf("TeardownError = %q, want teardown interrupted", res.TeardownError)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("Run took %v; teardown did not stop promptly", time.Since(start))
	}
}
