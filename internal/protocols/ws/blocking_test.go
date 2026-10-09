package ws_test

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
	"github.com/Arunraj-QA/loadtool/internal/protocols/ws"
)

// The blocking style: connect returns a socket; send, receive and close
// run in order, and each receive times the oldest unanswered send.
func TestBlockingEcho(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	h := protocoltest.New(t, ws.Module{})
	run(t, h, srv, `
var socket = ws.connect("WS/echo");
var opened = socket.status === 101 && socket.error === "" && !socket.closed;
socket.send("hello");
socket.send("world");
var first = socket.receive(5000), second = socket.receive(5000);
socket.sendBinary(new Uint8Array([7, 8]).buffer, { reply: false });
var bin = socket.receive(5000);
socket.close();
var res = { opened: opened, first: first, second: second, binLen: bin.byteLength, closed: socket.closed,
	afterClose: socket.receive(100), sentAfterClose: socket.send("late") };`)
	got := h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
	if !got.Get("opened").ToBoolean() || got.Get("first").String() != "hello" || got.Get("second").String() != "world" ||
		got.Get("binLen").ToInteger() != 2 || !got.Get("closed").ToBoolean() {
		t.Errorf("result = %v", got.Export())
	}
	if !goja_isNull(got.Get("afterClose").Export()) || got.Get("sentAfterClose").ToBoolean() {
		t.Errorf("after close: receive %v, send %v", got.Get("afterClose"), got.Get("sentAfterClose"))
	}
	// Two timed sends (the binary one opted out); every message counted.
	if l := h.Family(ws.MetricMsgLatency); l.Count != 2 {
		t.Errorf("latency samples = %d, want 2", l.Count)
	}
	if h.Family(ws.MetricMsgsSent).Count != 3 || h.Family(ws.MetricMsgsReceived).Count != 3 {
		t.Errorf("sent %d received %d", h.Family(ws.MetricMsgsSent).Count, h.Family(ws.MetricMsgsReceived).Count)
	}
	// The session ended cleanly; the send after close is a send failure.
	if f := h.Family(ws.MetricSessionFailed); f.Count != 1 || f.Trues != 0 {
		t.Errorf("session failed = %d of %d", f.Trues, f.Count)
	}
	if h.Family(ws.MetricSessionDuration).Count != 1 || h.Family(ws.MetricErrors).Count != 1 {
		t.Errorf("duration %d, errors %d", h.Family(ws.MetricSessionDuration).Count, h.Family(ws.MetricErrors).Count)
	}
	if w := h.VU().Warnings(); len(w) != 0 {
		t.Errorf("warnings for a socket the script closed: %q", w)
	}
	waitGoroutines(t, before)
}

func goja_isNull(v any) bool { return v == nil }

// receive returns null after its timeout, counts a ws_errors with
// error_code "timeout", and the socket stays usable.
func TestBlockingReceiveTimeout(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	run(t, h, srv, `
var socket = ws.connect("WS/silent");
var start = Date.now();
var msg = socket.receive(50);
var res = { isNull: msg === null, waited: Date.now() - start, code: socket.error_code, open: !socket.closed,
	sent: socket.send("still open") };
socket.close();`)
	got := h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
	if !got.Get("isNull").ToBoolean() || got.Get("code").String() != "timeout" || !got.Get("open").ToBoolean() || !got.Get("sent").ToBoolean() {
		t.Errorf("result = %v", got.Export())
	}
	if w := got.Get("waited").ToInteger(); w < 40 || w > 2000 {
		t.Errorf("waited %d ms for a 50 ms timeout", w)
	}
	if h.Family(ws.MetricErrors).Count != 1 {
		t.Errorf("ws_errors = %d, want 1", h.Family(ws.MetricErrors).Count)
	}
}

// A server close shows up as receive returning null with the session
// over; 1000 is clean, 4001 a failed session.
func TestBlockingServerClose(t *testing.T) {
	srv := server(t)
	for code, failed := range map[string]bool{"1000": false, "4001": true} {
		t.Run(code, func(t *testing.T) {
			h := protocoltest.New(t, ws.Module{})
			run(t, h, srv, `
var socket = ws.connect("WS/close/`+code+`");
socket.send("x", { reply: false });
var res = { msg: socket.receive(5000), closed: socket.closed, code: socket.error_code };`)
			got := h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
			wantCode := ""
			if failed {
				wantCode = "server"
			}
			if got.Get("msg").Export() != nil || !got.Get("closed").ToBoolean() || got.Get("code").String() != wantCode {
				t.Errorf("result = %v", got.Export())
			}
			if f := h.Family(ws.MetricSessionFailed); (f.Trues == 1) != failed || f.Count != 1 {
				t.Errorf("session failed = %d of %d", f.Trues, f.Count)
			}
		})
	}
}

// A failed handshake gives a closed socket with the error; send returns
// false and receive null, without throwing.
func TestBlockingConnectFailure(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	run(t, h, srv, `
var socket = ws.connect("WS/nope");
var res = { status: socket.status, code: socket.error_code, closed: socket.closed,
	sent: socket.send("x"), msg: socket.receive(10) };
socket.close();`)
	got := h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
	if got.Get("status").ToInteger() != 404 || got.Get("code").String() != "server" || !got.Get("closed").ToBoolean() ||
		got.Get("sent").ToBoolean() || got.Get("msg").Export() != nil {
		t.Errorf("result = %v", got.Export())
	}
	if f := h.Family(ws.MetricSessionFailed); f.Count != 1 || f.Trues != 1 {
		t.Errorf("session failed = %d of %d", f.Trues, f.Count)
	}
}

// A socket the script does not close is closed when the iteration ends,
// counted as a normal session, with a warning.
func TestBlockingSocketClosedAtIterationEnd(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	h := protocoltest.New(t, ws.Module{})
	run(t, h, srv, `
var a = ws.connect("WS/echo"), b = ws.connect("WS/echo");
a.send("1"); b.send("2");
var res = { a: a.receive(5000), b: b.receive(5000) };`) // neither closed
	got := h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
	if got.Get("a").String() != "1" || got.Get("b").String() != "2" {
		t.Errorf("result = %v", got.Export())
	}
	if f := h.Family(ws.MetricSessionFailed); f.Count != 2 || f.Trues != 0 {
		t.Errorf("session failed = %d of %d, want 0 of 2", f.Trues, f.Count)
	}
	warned := false
	for _, w := range h.VU().Warnings() {
		warned = warned || strings.Contains(w, "still open when the iteration ended")
	}
	if !warned {
		t.Errorf("no warning; warnings %q", h.VU().Warnings())
	}
	waitGoroutines(t, before)
}

// When the test ends during receive, it returns promptly, nothing about
// the session is recorded, and nothing is left running.
func TestBlockingCancellation(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	h := protocoltest.New(t, ws.Module{})
	ctx, cancel := context.WithCancel(context.Background())
	h.VU().SetContext(ctx)
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, _ = h.Run(strings.ReplaceAll(`var s = ws.connect("WS/silent"); s.send("x"); s.receive(60000);`, "WS", wsURL(srv, "")))
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("receive took %v after cancellation", took)
	}
	if f := h.Family(ws.MetricSessionFailed); f.Count != 0 {
		t.Errorf("a cancelled session was recorded: %+v", f)
	}
	waitGoroutines(t, before)
}

// Many VUs use blocking sockets at once (run with -race).
func TestBlockingConcurrentVUs(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			h := protocoltest.New(t, ws.Module{})
			_, err := h.Run(strings.ReplaceAll(`
for (var s = 0; s < 5; s++) {
	var socket = ws.connect("WS/echo");
	for (var i = 0; i < 10; i++) {
		socket.send("m" + i);
		if (socket.receive(5000) !== "m" + i) throw new Error("bad echo");
	}
	socket.close();
}`, "WS", wsURL(srv, "")))
			if err != nil {
				t.Error(err)
				return
			}
			if got := h.Family(ws.MetricMsgLatency).Count; got != 50 {
				t.Errorf("latency samples = %d, want 50", got)
			}
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	waitGoroutines(t, before)
}
