package grpc_test

import (
	"context"
	"strings"
	"testing"
	"time"

	lgrpc "github.com/Arunraj-QA/loadtool/internal/protocols/grpc"
)

// The coverage matrix of the gRPC module: every call kind with each way
// of describing methods (.proto and reflection), errors, timeouts and
// cancellation. The other tests in this package cover the remaining cells.

const connectReflect = `
var client = new grpc.Client();
var conn = client.connect("ADDR", { plaintext: true, reflect: true });
if (conn.error !== "") throw new Error("connect: " + conn.error);
`

// Every streaming kind works with methods described by reflection.
func TestStreamsWithReflection(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, connectReflect+`
var server = client.stream("greeter.Greeter/LotsOfReplies");
server.send({ name: "r", count: 2 }); server.closeSend();
var n = 0; while (server.recv() !== null) n++;

var cs = client.stream("greeter.Greeter/LotsOfGreetings");
cs.send({ name: "a" }); cs.send({ name: "b" }); cs.closeSend();
var summary = cs.recv().message; cs.recv();

var bidi = client.stream("greeter.Greeter/Chat");
bidi.send({ name: "z" });
var echoed = bidi.recv().message;
bidi.closeSend(); bidi.recv();

var res = { n: n, summary: summary, echoed: echoed, statuses: [server.status, cs.status, bidi.status].join(",") };`)
	if res.Get("n").ToInteger() != 2 || res.Get("summary").String() != "Hello, a, b" ||
		res.Get("echoed").String() != "Hello, z" || res.Get("statuses").String() != "0,0,0" {
		t.Errorf("result = %v", res.Export())
	}
	if f := h.Family(lgrpc.MetricStreamFailed); f.Count != 3 || f.Trues != 0 {
		t.Errorf("stream failed = %d of %d", f.Trues, f.Count)
	}
}

// A stream that ends with a status other than OK: recv returns null, the
// stream holds the status and error_code, and grpc_stream_failed counts
// it, for every streaming kind.
func TestStreamErrors(t *testing.T) {
	addr := server(t, 0)
	h := harness(t)
	res := run(t, h, addr, connectProto+`
// Server streaming: two replies, then NOT_FOUND (5).
var server = client.stream("greeter.Greeter/LotsOfReplies");
server.send({ name: "s", count: 2, failCode: 5 }); server.closeSend();
var got = 0; while (server.recv() !== null) got++;

// Client streaming: INTERNAL (13) instead of the reply.
var cs = client.stream("greeter.Greeter/LotsOfGreetings");
cs.send({ name: "a" }); cs.send({ name: "b", failCode: 13 }); cs.closeSend();
var csReply = cs.recv();

// Bidirectional: the first request is answered, the second fails with
// PERMISSION_DENIED (7).
var bidi = client.stream("greeter.Greeter/Chat");
bidi.send({ name: "ok" });
var first = bidi.recv().message;
bidi.send({ name: "no", failCode: 7 });
var second = bidi.recv();

var res = {
	server: [got, server.status, server.status_text, server.error_code, server.closed].join(","),
	cs: [csReply, cs.status, cs.error_code, cs.error].join(","),
	bidi: [first, second, bidi.status, bidi.error_code].join(","),
	sendAfter: bidi.send({ name: "late" }),
};`)
	for k, want := range map[string]string{
		"server": "2,5,NotFound,server,true",
		"cs":     ",13,server,failed as asked (fail_code 13)",
		"bidi":   "Hello, ok,,7,server",
	} {
		if got := res.Get(k).String(); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if res.Get("sendAfter").ToBoolean() {
		t.Error("send on an ended stream returned true")
	}
	if f := h.Family(lgrpc.MetricStreamFailed); f.Count != 3 || f.Trues != 3 {
		t.Errorf("stream failed = %d of %d, want 3 of 3", f.Trues, f.Count)
	}
	if d := h.Family(lgrpc.MetricStreamDuration); d.Count != 3 || d.Failed != 3 {
		t.Errorf("stream durations = %d (%d failed)", d.Count, d.Failed)
	}
}

// A stream's timeout bounds the whole stream, for every streaming kind:
// a stream still waiting at its deadline ends with DEADLINE_EXCEEDED and
// error_code "timeout".
func TestStreamTimeouts(t *testing.T) {
	addr := server(t, 300*time.Millisecond) // each LotsOfReplies reply waits 300 ms
	h := harness(t)
	start := time.Now()
	res := run(t, h, addr, connectProto+`
// Server streaming: the first reply comes after 300 ms, the deadline at 100.
var server = client.stream("greeter.Greeter/LotsOfReplies", { timeout: 100 });
server.send({ name: "s", count: 3 }); server.closeSend();
var serverMsg = server.recv();

// Client streaming: the server waits for closeSend, which never comes.
var cs = client.stream("greeter.Greeter/LotsOfGreetings", { timeout: "100ms" });
cs.send({ name: "a" });
var csMsg = cs.recv();

// Bidirectional: nothing is sent, so nothing comes back.
var bidi = client.stream("greeter.Greeter/Chat", { timeout: "100ms" });
var bidiMsg = bidi.recv();

var res = {
	msgs: [serverMsg, csMsg, bidiMsg].join(","),
	statuses: [server.status, cs.status, bidi.status].join(","),
	codes: [server.error_code, cs.error_code, bidi.error_code].join(","),
};`)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("three 100 ms timeouts took %v", took)
	}
	if res.Get("msgs").String() != ",," || res.Get("statuses").String() != "4,4,4" || res.Get("codes").String() != "timeout,timeout,timeout" {
		t.Errorf("result = %v", res.Export())
	}
	if f := h.Family(lgrpc.MetricStreamFailed); f.Trues != 3 {
		t.Errorf("stream failed = %d of %d, want 3", f.Trues, f.Count)
	}
}

// When the test ends during a unary call, invoke returns promptly and the
// call is not recorded.
func TestUnaryCancellation(t *testing.T) {
	addr := server(t, 5*time.Second)
	h := harness(t)
	if _, err := h.Run(strings.ReplaceAll(connectProto, "ADDR", addr)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.VU().SetContext(ctx)
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, _ = h.Run(`client.invoke("greeter.Greeter/SayHello", { name: "slow" })`)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("invoke took %v after cancellation", took)
	}
	if n := h.Family(lgrpc.MetricReqs).Count; n != 0 {
		t.Errorf("a cancelled call was recorded (%d)", n)
	}
}
