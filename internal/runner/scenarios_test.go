package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// vuServer records, per path, which __VU values requested it.
type vuServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen map[string]map[string]bool // path -> set of VU ids
}

func newVUServer(t *testing.T) *vuServer {
	t.Helper()
	s := &vuServer{seen: make(map[string]map[string]bool)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.seen[r.URL.Path] == nil {
			s.seen[r.URL.Path] = make(map[string]bool)
		}
		s.seen[r.URL.Path][r.URL.Query().Get("vu")] = true
		s.mu.Unlock()
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *vuServer) vus(path string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[path]
}

func runScript(t *testing.T, src string, o config.Overrides, warn func(string)) (report.Result, error) {
	t.Helper()
	return Run(context.Background(), Params{
		Config: config.Config{Script: scriptFile(t, src), GracefulStop: time.Second}, Overrides: o, Warn: warn,
	})
}

// Scenarios run their own exec functions, concurrently, with __VU unique
// across scenarios; no default export is needed when none runs it.
func TestScenariosRunTheirExecFunctions(t *testing.T) {
	srv := newVUServer(t)
	res, err := runScript(t, `import http from "loadtool/http";
export const options = { scenarios: {
	browse: { executor: "constant-vus", vus: 2, duration: "150ms", exec: "browse" },
	orders: { executor: "constant-arrival-rate", rate: 50, timeUnit: "1s", duration: "150ms", preAllocatedVUs: 2, exec: "order" },
} };
export function browse() { http.get("`+srv.URL+`/browse?vu=" + __VU); }
export function order() { http.get("`+srv.URL+`/order?vu=" + __VU); }`, config.Overrides{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	browse, order := srv.vus("/browse"), srv.vus("/order")
	// browse comes first by name: VUs 1-2; orders gets VUs 3-4.
	if !browse["1"] || !browse["2"] || len(browse) != 2 {
		t.Errorf("browse ran on VUs %v, want 1 and 2", browse)
	}
	for vu := range order {
		if vu != "3" && vu != "4" {
			t.Errorf("order ran on VU %s, want 3 or 4", vu)
		}
	}
	if len(order) == 0 {
		t.Error("the arrival-rate scenario never ran")
	}
	if len(res.Scenarios) != 2 || !strings.HasPrefix(res.Scenarios[1], "orders: constant-arrival-rate") {
		t.Errorf("Scenarios = %q", res.Scenarios)
	}
	if res.VUs != 4 {
		t.Errorf("VUs = %d, want 4", res.VUs)
	}
}

func TestStagesShorthandRamps(t *testing.T) {
	srv := newVUServer(t)
	res, err := runScript(t, `import http from "loadtool/http";
export const options = { stages: [{ duration: "100ms", target: 3 }, { duration: "100ms", target: 3 }] };
export default function () { http.get("`+srv.URL+`/x?vu=" + __VU); }`, config.Overrides{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.vus("/x"); len(got) != 3 {
		t.Errorf("VUs that ran: %v, want 3 (the stages reach 3)", got)
	}
	if res.VUs != 3 || res.Duration != 200*time.Millisecond || len(res.Scenarios) != 1 {
		t.Errorf("VUs=%d Duration=%v Scenarios=%q", res.VUs, res.Duration, res.Scenarios)
	}
}

// Dropped arrival-rate starts reach the summary and the threshold metric.
func TestDroppedIterationsReachThresholds(t *testing.T) {
	res, err := runScript(t, `import { sleep } from "loadtool";
export const options = {
	scenarios: { slow: { executor: "constant-arrival-rate", rate: 100, duration: "200ms", preAllocatedVUs: 1 } },
	thresholds: { dropped_iterations: ["count==0"] },
};
export default function () { sleep(0.05); }`, config.Overrides{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.DroppedIterations == 0 {
		t.Fatalf("DroppedIterations = 0 with one VU taking 50ms at a 10ms interval")
	}
	if got := res.Summary.Iterations + res.Summary.DroppedIterations; got != 20 {
		t.Errorf("iterations + dropped = %d, want 20 starts", got)
	}
	if len(res.Thresholds) != 1 || res.Thresholds[0].Passed {
		t.Errorf("Thresholds = %+v, want dropped_iterations count==0 to fail", res.Thresholds)
	}
}

// Problems with the functions scenarios run are found before setup.
func TestScenarioExecErrorsStopBeforeSetup(t *testing.T) {
	srv := newEventServer(t)
	tests := []struct{ name, options, want string }{
		{"missing export", `scenarios: { a: { executor: "constant-vus", duration: "1s", exec: "chekout" } }`, `exec "chekout" is not exported`},
		{"no default", `scenarios: { a: { executor: "constant-vus", duration: "1s" } }`, `scenario "a" runs the default function: script must export a default function`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runScript(t, `import http from "loadtool/http";
export const options = { `+tt.options+` };
export function setup() { http.get("`+srv.URL+`/login"); }
export function checkout() {}`, config.Overrides{}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if got := srv.log(); got != "" {
				t.Errorf("server saw %q: setup ran", got)
			}
		})
	}
}

// A typed --vus replaces the script's scenarios, with a warning.
func TestTypedVUsReplaceScenarios(t *testing.T) {
	srv := newVUServer(t)
	var warnings []string
	res, err := runScript(t, `import http from "loadtool/http";
export const options = { scenarios: { a: { executor: "constant-arrival-rate", rate: 5, duration: "1m", preAllocatedVUs: 9 } } };
export default function () { http.get("`+srv.URL+`/x?vu=" + __VU); }`,
		config.Overrides{VUs: intp(2), Duration: durp(100 * time.Millisecond)},
		func(msg string) { warnings = append(warnings, msg) })
	if err != nil {
		t.Fatal(err)
	}
	if res.VUs != 2 || res.Duration != 100*time.Millisecond || res.Scenarios != nil {
		t.Errorf("VUs=%d Duration=%v Scenarios=%q, want the 2-VU shorthand", res.VUs, res.Duration, res.Scenarios)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "replace the script's scenarios") {
		t.Errorf("warnings = %q", warnings)
	}
}

// Keep-alive reuse is the default; options.noConnectionReuse opens one
// connection per request (ADR-009).
func TestConnectionReuseOption(t *testing.T) {
	for _, tt := range []struct {
		option string
		reuse  bool
	}{{"", true}, {"noConnectionReuse: true", false}} {
		t.Run(fmt.Sprintf("reuse=%v", tt.reuse), func(t *testing.T) {
			var conns atomic.Int64
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					conns.Add(1)
				}
			}
			srv.Start()
			t.Cleanup(srv.Close)
			res, err := runScript(t, `import http from "loadtool/http";
export const options = { vus: 2, duration: "100ms", `+tt.option+` };
export default function () { http.get("`+srv.URL+`"); }`, config.Overrides{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			n, reqs := conns.Load(), int64(res.Summary.Requests)
			if reqs < 4 {
				t.Fatalf("only %d requests", reqs)
			}
			if tt.reuse && n > 2 {
				t.Errorf("%d connections for %d requests by 2 VUs, want at most 2", n, reqs)
			}
			if !tt.reuse && n < reqs {
				t.Errorf("%d connections for %d requests, want one per request", n, reqs)
			}
		})
	}
}
