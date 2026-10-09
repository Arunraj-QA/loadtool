package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/protocols/grpc/grpctest"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

func grpcServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	if err := grpctest.Register(srv, 0); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

// One script mixes HTTP and gRPC: a client made and loaded in top-level
// code, connected once per VU, unary calls and a stream per iteration.
// Both are measured apart, gRPC thresholds work, setup's call is not
// counted, and every connection is closed after the run.
func TestRunMixesHTTPAndGRPC(t *testing.T) {
	httpSrv := okServer(t)
	addr := grpcServer(t)
	before := runtime.NumGoroutine()
	protoDir, _ := filepath.Abs(filepath.Join("..", "..", "examples", "proto"))
	path := scriptFile(t, `import http from "loadtool/http";
import grpc from "loadtool/grpc";
import { check } from "loadtool";

const client = new grpc.Client();
client.load(["`+filepath.ToSlash(protoDir)+`"], "greeter.proto");

export const options = {
	thresholds: {
		grpc_req_failed: ["rate==0"],
		grpc_req_duration: ["p(95)<1000"],
		grpc_stream_failed: ["rate==0"],
		http_req_failed: ["rate==0"],
	},
};

export function setup() {
	const c = new grpc.Client();
	c.load(["`+filepath.ToSlash(protoDir)+`"], "greeter.proto");
	c.connect("`+addr+`", { plaintext: true });
	c.invoke("greeter.Greeter/SayHello", { name: "setup" }); // not counted
	c.close();
}

export default function () {
	if (__ITER === 0) {
		const conn = client.connect("`+addr+`", { plaintext: true });
		if (conn.error !== "") throw new Error(conn.error);
	}
	http.get("`+httpSrv.URL+`");
	const r = client.invoke("greeter.Greeter/SayHello", { name: "VU" + __VU });
	check(r, { "grpc ok": (x) => x.status === 0 && x.message.message === "Hello, VU" + __VU });
	const s = client.stream("greeter.Greeter/LotsOfReplies");
	s.send({ name: "s", count: 2 }); s.closeSend();
	let n = 0; while (s.recv() !== null) n++;
	check(n, { "stream complete": (x) => x === 2 });
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
	calls, _ := s.Family("grpc_reqs")
	streams, _ := s.Family("grpc_streams")
	if calls.Count == 0 || calls.Count != s.Requests || streams.Count != calls.Count {
		t.Errorf("%d gRPC calls, %d streams, %d HTTP requests; want one of each per iteration (setup not counted)", calls.Count, streams.Count, s.Requests)
	}
	for _, th := range res.Thresholds {
		if !th.Passed {
			t.Errorf("threshold %s %s failed", th.Metric, th.Expr)
		}
	}
	for _, c := range s.Checks {
		if c.Fails != 0 {
			t.Errorf("check %q failed %d times", c.Name, c.Fails)
		}
	}
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
	for _, name := range []string{"grpc_req_duration", "grpc_reqs", "grpc_req_failed", "grpc_streams", "grpc_stream_duration", "grpc_stream_msgs_received"} {
		if _, ok := doc.Metrics[name]; !ok {
			t.Errorf("JSON summary misses %s", name)
		}
	}
	b.Reset()
	if err := report.HTML(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<td><code>grpc_req_duration</code></td>") {
		t.Error("HTML report misses grpc_req_duration")
	}
	// Every VU's connection was closed by the runner.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+4 {
		t.Errorf("goroutines: %d after the run, %d before", n, before)
	}
}
