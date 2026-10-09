package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// mixedServer serves HTTP on / and a WebSocket echo on /ws.
func mixedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			w.Write([]byte(`{"ok":true}`))
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if c.Write(r.Context(), typ, data) != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// One script mixes HTTP and WebSocket in each iteration (Phase 2 goal
// 6): both are measured, apart, thresholds on WebSocket metrics work,
// setup's WebSocket session is not counted, and every VU's resources are
// released after the run.
func TestRunMixesHTTPAndWebSocket(t *testing.T) {
	srv := mixedServer(t)
	before := runtime.NumGoroutine()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	path := scriptFile(t, `import http from "loadtool/http";
import ws from "loadtool/ws";
import { check } from "loadtool";

export const options = {
	thresholds: {
		ws_session_failed: ["rate==0"],
		ws_msg_latency: ["p(95)<1000"],
		ws_msgs_received: ["count>0"],
		http_req_failed: ["rate==0"],
	},
};

export function setup() {
	// Counted nowhere: setup and teardown are not part of the results.
	ws.connect("`+wsURL+`", (socket) => {
		socket.on("open", () => socket.send("setup", { reply: true }));
		socket.on("message", () => socket.close());
	});
}

export default function () {
	const res = http.get("`+srv.URL+`/");
	check(res, { "http ok": (r) => r.status === 200 });
	let echoed = 0;
	const session = ws.connect("`+wsURL+`", {}, (socket) => {
		socket.on("open", () => {
			for (let i = 0; i < 3; i++) socket.send("m" + i, { reply: true });
		});
		socket.on("message", () => { if (++echoed === 3) socket.close(); });
	});
	check(session, { "ws ok": (s) => s.error === "" && s.status === 101 });
}`)
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(4), Duration: durp(300 * time.Millisecond)},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	if s.Requests == 0 || s.Failures != 0 {
		t.Errorf("HTTP: %d requests, %d failed", s.Requests, s.Failures)
	}
	sessions, _ := s.Family("ws_sessions")
	received, _ := s.Family("ws_msgs_received")
	latency, _ := s.Family("ws_msg_latency")
	if sessions.Count == 0 || received.Count != 3*sessions.Count || latency.Count != received.Count {
		t.Errorf("ws: %d sessions, %d received, %d latency samples; want 3 per session", sessions.Count, received.Count, latency.Count)
	}
	// Each iteration made one HTTP request and one session; setup's
	// session is not counted.
	if s.Requests != sessions.Count {
		t.Errorf("%d HTTP requests but %d sessions; setup's session must not be counted", s.Requests, sessions.Count)
	}
	for _, th := range res.Thresholds {
		if !th.Passed {
			t.Errorf("threshold %s %s failed (observed %v, no data %v)", th.Metric, th.Expr, th.Observed, th.NoData)
		}
	}
	for _, c := range s.Checks {
		if c.Fails != 0 {
			t.Errorf("check %q failed %d times", c.Name, c.Fails)
		}
	}

	// The JSON summary carries the families, and HTML shows them.
	var b bytes.Buffer
	if err := report.JSON(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Metrics map[string]json.RawMessage `json:"metrics"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ws_connecting", "ws_sessions", "ws_session_failed", "ws_session_duration", "ws_msgs_sent", "ws_msgs_received", "ws_msg_latency"} {
		if _, ok := doc.Metrics[name]; !ok {
			t.Errorf("JSON summary misses %s", name)
		}
	}
	if _, ok := doc.Metrics["ws_errors"]; ok {
		t.Error("JSON summary has ws_errors, which recorded nothing")
	}
	b.Reset()
	if err := report.HTML(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<td><code>ws_msg_latency</code></td>") {
		t.Error("HTML report misses ws_msg_latency")
	}

	// Every reader goroutine and connection is gone.
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines: %d after the run, %d before\n%s", n, before, buf[:runtime.Stack(buf, true)])
	}
}

// A threshold on a WebSocket metric fails the run (exit 99), and a
// threshold on a family of a module the script does not import is an
// unknown metric.
func TestWebSocketThresholds(t *testing.T) {
	srv := mixedServer(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	path := scriptFile(t, `import ws from "loadtool/ws";
export const options = { thresholds: { ws_msgs_received: ["count>1000000"] } };
export default function () {
	ws.connect("`+wsURL+`", (socket) => {
		socket.on("open", () => socket.send("x"));
		socket.on("message", () => socket.close());
	});
}`)
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(1), Duration: durp(100 * time.Millisecond)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := report.Verdict(res); v.ExitCode != report.ExitThresholdsFailed {
		t.Errorf("verdict %+v, want exit code 99", v)
	}

	httpOnly := scriptFile(t, `import http from "loadtool/http";
export const options = { thresholds: { ws_msgs_received: ["count>0"] } };
export default function () {}`)
	_, err = Run(context.Background(), Params{
		Config:    config.Config{Script: httpOnly, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(1), Duration: durp(50 * time.Millisecond)},
	})
	if err == nil || !strings.Contains(err.Error(), `unknown metric "ws_msgs_received"`) {
		t.Errorf("error = %v, want unknown metric", err)
	}
}

// ws.connect in top-level code is an error before the test starts, as
// http requests are.
func TestWebSocketInTopLevelCode(t *testing.T) {
	path := scriptFile(t, `import ws from "loadtool/ws";
ws.connect("ws://127.0.0.1:1/", () => {});
export default function () {}`)
	_, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(1), Duration: durp(50 * time.Millisecond)},
	})
	if err == nil || !strings.Contains(err.Error(), "top-level code") {
		t.Errorf("error = %v, want a top-level code error", err)
	}
}

// The blocking style, as a user wrote it: "loadtool/websocket", an async
// default function, send then receive, and a check on the reply.
func TestRunBlockingWebSocketStyle(t *testing.T) {
	srv := mixedServer(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	path := scriptFile(t, `import ws from "loadtool/websocket";
import { check } from "loadtool";

export const options = { thresholds: { ws_msg_latency: ["p(95)<1000"], checks: ["rate==1"] } };

export default async function () {
	const socket = ws.connect("`+wsURL+`");
	socket.send("hello");
	const response = socket.receive(5000);
	check(response, {
		"message received": (r) => r != null,
		"echoed": (r) => r === "hello",
	});
	socket.close();
}`)
	var warnings []string
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(3), Duration: durp(200 * time.Millisecond)},
		Warn:      func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	sessions, _ := s.Family("ws_sessions")
	latency, _ := s.Family("ws_msg_latency")
	if sessions.Count == 0 || latency.Count != sessions.Count {
		t.Errorf("%d sessions, %d latency samples; want one per session", sessions.Count, latency.Count)
	}
	for _, th := range res.Thresholds {
		if !th.Passed {
			t.Errorf("threshold %s %s failed", th.Metric, th.Expr)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("warnings: %q", warnings)
	}
}
