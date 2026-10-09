package grpc_test

import (
	"context"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
	"google.golang.org/grpc"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
	lgrpc "github.com/Arunraj-QA/loadtool/internal/protocols/grpc"
	"github.com/Arunraj-QA/loadtool/internal/protocols/grpc/grpctest"
)

// protoDir is the directory of the greeter.proto fixture.
var protoDir, _ = filepath.Abs(filepath.Join("..", "..", "..", "examples", "proto"))

// server starts the greeter test service (with reflection) on a free
// port; SayHello waits delay.
func server(t testing.TB, delay time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	if err := grpctest.Register(srv, delay); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

// harness returns a VU with the grpc module whose script directory is the
// fixture's, so client.load(["."], "greeter.proto") works.
func harness(t testing.TB) *protocoltest.Harness {
	return protocoltest.New(t, lgrpc.Module{}, protocoltest.WithEnv(func(e *protocol.RunEnv) { e.Dir = protoDir }))
}

// run runs src with ADDR replaced and returns the global res.
func run(t *testing.T, h *protocoltest.Harness, addr, src string) *goja.Object {
	t.Helper()
	if _, err := h.Run(strings.ReplaceAll(src, "ADDR", addr)); err != nil {
		t.Fatalf("script: %v", err)
	}
	rt := h.VU().Runtime()
	return rt.Get("res").ToObject(rt)
}

func waitGoroutines(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > n {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d, want at most %d\n%s", runtime.NumGoroutine(), n, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const connectProto = `
var client = new grpc.Client();
client.load(["."], "greeter.proto");
var conn = client.connect("ADDR", { plaintext: true });
if (conn.error !== "") throw new Error("connect: " + conn.error);
`

func TestUnaryWithProtoFile(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, connectProto+`
var r = client.invoke("greeter.Greeter/SayHello", { name: "Ada" }, { metadata: { "x-request-id": "42" }, timeout: "2s" });
var res = { status: r.status, text: r.status_text, msg: r.message.message, index: r.message.index,
	reqID: r.headers["x-request-id"], servedBy: r.trailers["x-served-by"], err: r.error, code: r.error_code,
	ms: r.timings.duration };`)
	want := map[string]any{"status": int64(0), "text": "OK", "msg": "Hello, Ada", "index": int64(0),
		"reqID": "42", "servedBy": "grpctest", "err": "", "code": ""}
	for k, v := range want {
		if got := res.Get(k).Export(); got != v {
			t.Errorf("%s = %v (%T), want %v", k, got, got, v)
		}
	}
	if d := h.Family(lgrpc.MetricReqDuration); d.Count != 1 || d.Failed != 0 {
		t.Errorf("req duration = %+v", d)
	}
	if h.Family(lgrpc.MetricReqs).Count != 1 || h.Family(lgrpc.MetricReqFailed).Trues != 0 {
		t.Error("calls or failures miscounted")
	}
}

// Reflection: no .proto file, the server describes its methods.
func TestUnaryWithReflection(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, `
var client = new grpc.Client();
var conn = client.connect("ADDR", { plaintext: true, reflect: true });
var r = client.invoke("greeter.Greeter/SayHello", { name: "Reflect" });
var res = { connErr: conn.error, msg: r.message.message };`)
	if res.Get("connErr").String() != "" || res.Get("msg").String() != "Hello, Reflect" {
		t.Errorf("result = %v", res.Export())
	}
}

func TestStatusErrorsAndDeadlines(t *testing.T) {
	addr := server(t, 300*time.Millisecond)
	h := harness(t)
	res := run(t, h, addr, connectProto+`
var notFound = client.invoke("greeter.Greeter/Fail", { code: 5, message: "no such greeter" });
var unavailable = client.invoke("greeter.Greeter/Fail", { code: 14, message: "down" });
var slow = client.invoke("greeter.Greeter/SayHello", { name: "x" }, { timeout: 50 });
var res = {
	nf: [notFound.status, notFound.status_text, notFound.error, notFound.error_code, notFound.message],
	un: [unavailable.status, unavailable.error_code],
	slow: [slow.status, slow.status_text, slow.error_code],
};`)
	nf := res.Get("nf").Export().([]any)
	if nf[0] != int64(5) || nf[1] != "NotFound" || nf[2] != "no such greeter" || nf[3] != "server" || nf[4] != nil {
		t.Errorf("not found = %v", nf)
	}
	if un := res.Get("un").Export().([]any); un[0] != int64(14) || un[1] != "closed" {
		t.Errorf("unavailable = %v", un)
	}
	if slow := res.Get("slow").Export().([]any); slow[0] != int64(4) || slow[2] != "timeout" {
		t.Errorf("deadline = %v", slow)
	}
	if f := h.Family(lgrpc.MetricReqFailed); f.Count != 3 || f.Trues != 3 {
		t.Errorf("failed = %d of %d", f.Trues, f.Count)
	}
}

func TestServerStreaming(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, connectProto+`
var s = client.stream("greeter.Greeter/LotsOfReplies");
s.send({ name: "Ada", count: 4 });
s.closeSend();
var got = [];
for (var m = s.recv(); m !== null; m = s.recv()) got.push(m.index + ":" + m.message);
var res = { got: got.join("|"), status: s.status, closed: s.closed, code: s.error_code };`)
	if res.Get("got").String() != "0:Hello 0, Ada|1:Hello 1, Ada|2:Hello 2, Ada|3:Hello 3, Ada" ||
		res.Get("status").ToInteger() != 0 || !res.Get("closed").ToBoolean() || res.Get("code").String() != "" {
		t.Errorf("result = %v", res.Export())
	}
	if h.Family(lgrpc.MetricStreams).Count != 1 || h.Family(lgrpc.MetricStreamMsgsSent).Count != 1 ||
		h.Family(lgrpc.MetricStreamMsgsReceived).Count != 4 || h.Family(lgrpc.MetricStreamDuration).Count != 1 ||
		h.Family(lgrpc.MetricStreamFailed).Trues != 0 {
		t.Error("stream metrics miscounted")
	}
}

func TestClientStreaming(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, connectProto+`
var s = client.stream("greeter.Greeter/LotsOfGreetings");
["a", "b", "c"].forEach(function (n) { s.send({ name: n }); });
s.closeSend();
var reply = s.recv(), after = s.recv();
var res = { msg: reply.message, index: reply.index, after: after, status: s.status };`)
	if res.Get("msg").String() != "Hello, a, b, c" || res.Get("index").ToInteger() != 3 ||
		res.Get("after").Export() != nil || res.Get("status").ToInteger() != 0 {
		t.Errorf("result = %v", res.Export())
	}
}

func TestBidirectionalStreaming(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, connectProto+`
var s = client.stream("greeter.Greeter/Chat", { timeout: "5s" });
var got = [];
["x", "y", "z"].forEach(function (n) { s.send({ name: n }); got.push(s.recv().message); });
s.closeSend();
var res = { got: got.join("|"), end: s.recv(), status: s.status };`)
	if res.Get("got").String() != "Hello, x|Hello, y|Hello, z" || res.Get("end").Export() != nil || res.Get("status").ToInteger() != 0 {
		t.Errorf("result = %v", res.Export())
	}
	if h.Family(lgrpc.MetricStreamMsgsSent).Count != 3 || h.Family(lgrpc.MetricStreamMsgsReceived).Count != 3 {
		t.Error("bidi messages miscounted")
	}
}

// Failures that never reach the server are "invalid" results; misuse
// throws; a refused connection is a connect error, not a throw.
func TestInvalidCallsAndMisuse(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, `
var unconnected = new grpc.Client();
unconnected.load(["."], "greeter.proto");
var notConnected = unconnected.invoke("greeter.Greeter/SayHello", {});
`+connectProto+`
var unknown = client.invoke("greeter.Greeter/Nope", {});
var badField = client.invoke("greeter.Greeter/SayHello", { nosuchfield: 1 });
var refused = new grpc.Client().connect("127.0.0.1:1", { plaintext: true, timeout: "3s" });
var res = { nc: notConnected.error_code, unknown: unknown.error_code, bad: badField.error_code,
	refused: refused.error_code, refusedErr: refused.error };`)
	for k, want := range map[string]string{"nc": "invalid", "unknown": "invalid", "bad": "invalid", "refused": "dial"} {
		if got := res.Get(k).String(); got != want {
			t.Errorf("%s = %q, want %q (result %v)", k, got, want, res.Export())
		}
	}
	// invalid calls are counted as failed calls without a duration.
	if f := h.Family(lgrpc.MetricReqFailed); f.Trues != 3 || h.Family(lgrpc.MetricReqDuration).Count != 0 {
		t.Errorf("failed %d of %d, durations %d", f.Trues, f.Count, h.Family(lgrpc.MetricReqDuration).Count)
	}
	for src, want := range map[string]string{
		connectProto + `client.invoke("greeter.Greeter/Chat", {})`: "is a streaming method; use client.stream",
		connectProto + `client.stream("greeter.Greeter/SayHello")`: "is a unary method; use client.invoke",
		connectProto + `client.invoke()`:                           "a method",
	} {
		if _, err := h.Run(strings.ReplaceAll(src, "ADDR", addr)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want %q", err, want)
		}
	}
}

// new grpc.Client() and load are allowed in top-level code; network calls
// are not.
func TestTopLevelCode(t *testing.T) {
	h := harness(t)
	h.VU().SetContext(nil)
	if _, err := h.Run(`var c = new grpc.Client(); c.load(["."], "greeter.proto");`); err != nil {
		t.Fatalf("top-level load: %v", err)
	}
	if _, err := h.Run(`c.connect("127.0.0.1:1", { plaintext: true })`); err == nil || !strings.Contains(err.Error(), "top-level code") {
		t.Errorf("top-level connect: %v", err)
	}
	if _, err := h.Run(`new grpc.Client().load(["."], "missing.proto")`); err == nil || !strings.Contains(err.Error(), "client.load") {
		t.Errorf("missing .proto: %v", err)
	}
}

// When the test ends during a blocked recv, it returns promptly, nothing
// is recorded for the stream, and nothing is left running.
func TestCancellation(t *testing.T) {
	addr := server(t, 0)
	before := runtime.NumGoroutine()
	h := harness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.VU().SetContext(ctx)
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	_, _ = h.Run(strings.ReplaceAll(connectProto+`
var s = client.stream("greeter.Greeter/Chat", { timeout: "60s" });
s.recv(); // the server answers only after a send: blocks`, "ADDR", addr))
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("recv took %v after cancellation", took)
	}
	if f := h.Family(lgrpc.MetricStreamFailed); f.Count != 0 {
		t.Errorf("a cancelled stream was recorded: %+v", f)
	}
	_ = h.Close()
	waitGoroutines(t, before+8) // the server's goroutines stay until cleanup
}

// A stream left open is closed when the iteration ends, as a normal end,
// with a warning.
func TestStreamLeftOpen(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	run(t, h, addr, connectProto+`
var s = client.stream("greeter.Greeter/Chat");
s.send({ name: "a" });
var res = { first: s.recv().message };`)
	if f := h.Family(lgrpc.MetricStreamFailed); f.Count != 1 || f.Trues != 0 {
		t.Errorf("stream failed = %d of %d", f.Trues, f.Count)
	}
	warned := false
	for _, w := range h.VU().Warnings() {
		warned = warned || strings.Contains(w, "stream was still open")
	}
	if !warned {
		t.Errorf("no warning: %q", h.VU().Warnings())
	}
}

// Many VUs, each with its own client and connection, call at once (run
// with -race); closing the VUs releases every connection.
func TestConcurrentVUs(t *testing.T) {
	addr := server(t, 0)
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			h := harness(t)
			_, err := h.Run(strings.ReplaceAll(connectProto+`
for (var i = 0; i < 50; i++) {
	var r = client.invoke("greeter.Greeter/SayHello", { name: "n" + i });
	if (r.message.message !== "Hello, n" + i) throw new Error("bad reply " + r.error);
}
var s = client.stream("greeter.Greeter/LotsOfReplies");
s.send({ name: "s", count: 5 }); s.closeSend();
var n = 0; while (s.recv() !== null) n++;
if (n !== 5) throw new Error("stream got " + n);`, "ADDR", addr))
			if err != nil {
				t.Error(err)
				return
			}
			if got := h.Family(lgrpc.MetricReqs).Count; got != 50 {
				t.Errorf("calls = %d", got)
			}
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	waitGoroutines(t, before+8)
}
