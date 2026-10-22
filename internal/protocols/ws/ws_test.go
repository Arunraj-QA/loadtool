package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
	"github.com/Arunraj-QA/loadtool/internal/protocols/ws"
)

// server is a local WebSocket test server. Paths:
//
//	/echo        echoes every message (text and binary)
//	/close/<n>   closes with status n after the first message
//	/drop        drops the TCP connection after the first message
//	/silent      accepts and never answers or reads (send fails once
//	             buffers fill, or the session waits)
//	/cookie      sends the "session" cookie value as the first message
//	other        404 (the handshake fails)
func server(t testing.TB) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/echo" && path != "/drop" && path != "/silent" && path != "/cookie" && !strings.HasPrefix(path, "/close/") {
			http.NotFound(w, r)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		switch {
		case path == "/cookie":
			v := ""
			if ck, err := r.Cookie("session"); err == nil {
				v = ck.Value
			}
			_ = c.Write(ctx, websocket.MessageText, []byte(v))
			_, _, _ = c.Read(ctx)
		case path == "/silent":
			// Reads but never answers; returns when the client goes away
			// (a hijacked connection's request context is not cancelled).
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
			}
		case path == "/drop":
			_, _, _ = c.Read(ctx)
			// CloseNow drops the connection without a close frame.
		case strings.HasPrefix(path, "/close/"):
			_, _, _ = c.Read(ctx)
			var code int
			for _, ch := range strings.TrimPrefix(path, "/close/") {
				code = code*10 + int(ch-'0')
			}
			_ = c.Close(websocket.StatusCode(code), "bye")
		default:
			for {
				typ, data, err := c.Read(ctx)
				if err != nil {
					return
				}
				if err := c.Write(ctx, typ, data); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

// run runs src with WS replaced by the server's ws:// URL and returns the
// connect result it leaves in the global res.
func run(t *testing.T, h *protocoltest.Harness, srv *httptest.Server, src string) *goja.Object {
	t.Helper()
	src = strings.ReplaceAll(src, "WS", wsURL(srv, ""))
	if _, err := h.Run(src); err != nil {
		t.Fatalf("script: %v", err)
	}
	return h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
}

func str(o *goja.Object, k string) string { return o.Get(k).String() }

// waitGoroutines waits for the goroutine count to fall back to n, so a
// leaked reader or close goroutine fails the test.
func waitGoroutines(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > n {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d, want at most %d\n%s", runtime.NumGoroutine(), n, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEchoMessagesAndLatency(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	res := run(t, h, srv, `
var got = [];
var res = ws.connect("WS/echo", {}, function (socket) {
	socket.on("open", function () {
		socket.send("one", { reply: true });
		socket.send("two", { reply: true });
		socket.send("three"); // not timed
	});
	socket.on("message", function (data) {
		got.push(data);
		if (got.length === 3) socket.close();
	});
});`)
	if got := h.VU().Runtime().Get("got").String(); got != "one,two,three" {
		t.Errorf("messages = %q", got)
	}
	if str(res, "status") != "101" || str(res, "error") != "" || str(res, "error_code") != "" {
		t.Errorf("result: status %s error %q code %q", str(res, "status"), str(res, "error"), str(res, "error_code"))
	}
	if h.Family(ws.MetricMsgsSent).Count != 3 || h.Family(ws.MetricMsgsReceived).Count != 3 {
		t.Errorf("sent %d received %d", h.Family(ws.MetricMsgsSent).Count, h.Family(ws.MetricMsgsReceived).Count)
	}
	lat := h.Family(ws.MetricMsgLatency)
	// A local echo answers within the Windows clock's ~0.5 ms tick, so a
	// latency of 0 is possible; the count and an upper bound are checked.
	if lat.Count != 2 || lat.Failed != 0 || lat.Max > 5*time.Second {
		t.Errorf("latency = %+v, want 2 samples", lat)
	}
	if c := h.Family(ws.MetricConnecting); c.Count != 1 || c.Failed != 0 {
		t.Errorf("connecting = %+v", c)
	}
	if s := h.Family(ws.MetricSessionFailed); s.Count != 1 || s.Trues != 0 {
		t.Errorf("session failed = %d of %d", s.Trues, s.Count)
	}
	if h.Family(ws.MetricSessionDuration).Count != 1 || h.Family(ws.MetricSessions).Count != 1 {
		t.Error("session duration or count missing")
	}
}

func TestBinaryAndTimers(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	run(t, h, srv, `
var sizes = [], ticks = 0;
var res = ws.connect("WS/echo", function (socket) {
	socket.on("open", function () { socket.sendBinary(new Uint8Array([1, 2, 3]).buffer); });
	socket.on("message", function (data) { sizes.push(data.byteLength); });
	socket.setInterval(function () { ticks++; }, 10);
	socket.setTimeout(function () { socket.close(); }, 120);
});`)
	rt := h.VU().Runtime()
	if got := rt.Get("sizes").String(); got != "3" {
		t.Errorf("binary sizes = %s", got)
	}
	if ticks := rt.Get("ticks").ToInteger(); ticks < 3 {
		t.Errorf("interval ran %d times in 120 ms", ticks)
	}
}

func TestServerClose(t *testing.T) {
	srv := server(t)
	for _, tt := range []struct {
		code     string
		failed   bool
		wantCode string
	}{
		{"1000", false, ""},
		{"1001", false, ""},
		{"4001", true, "server"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			h := protocoltest.New(t, ws.Module{})
			res := run(t, h, srv, `
var closed = -1;
var res = ws.connect("WS/close/`+tt.code+`", function (socket) {
	socket.on("open", function () { socket.send("x"); });
	socket.on("close", function (code) { closed = code; });
});`)
			if got := h.VU().Runtime().Get("closed").String(); got != tt.code {
				t.Errorf("close code = %s", got)
			}
			if str(res, "error_code") != tt.wantCode {
				t.Errorf("error_code = %q, want %q (error %q)", str(res, "error_code"), tt.wantCode, str(res, "error"))
			}
			if s := h.Family(ws.MetricSessionFailed); (s.Trues == 1) != tt.failed {
				t.Errorf("session failed = %d of %d", s.Trues, s.Count)
			}
		})
	}
}

// A dropped connection is a receive error: "error" fires with
// error_code "closed", ws_errors counts it, and the session fails.
func TestReceiveFailure(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	res := run(t, h, srv, `
var errs = [], closed = -1;
var res = ws.connect("WS/drop", function (socket) {
	socket.on("open", function () { socket.send("x"); });
	socket.on("error", function (e) { errs.push(e.error_code); });
	socket.on("close", function (code) { closed = code; });
});`)
	rt := h.VU().Runtime()
	if got := rt.Get("errs").String(); got != "closed" {
		t.Errorf("error codes = %q", got)
	}
	if rt.Get("closed").ToInteger() != 1006 || str(res, "error_code") != "closed" {
		t.Errorf("closed %v, error_code %q", rt.Get("closed"), str(res, "error_code"))
	}
	if h.Family(ws.MetricErrors).Count != 1 || h.Family(ws.MetricSessionFailed).Trues != 1 {
		t.Error("the receive error was not recorded")
	}
}

// Sending after close() is a send failure: it returns false, fires
// "error" and counts in ws_errors.
func TestSendFailure(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	run(t, h, srv, `
var sent, errs = [];
var res = ws.connect("WS/echo", function (socket) {
	socket.on("open", function () { socket.close(); sent = socket.send("late"); });
	socket.on("error", function (e) { errs.push(e.error_code + ": " + e.error); });
});`)
	rt := h.VU().Runtime()
	if rt.Get("sent").ToBoolean() || rt.Get("errs").String() != "closed: the socket is closed" {
		t.Errorf("sent %v, errors %v", rt.Get("sent"), rt.Get("errs"))
	}
	if h.Family(ws.MetricErrors).Count != 1 {
		t.Error("the send failure was not counted")
	}
}

func TestConnectionFailures(t *testing.T) {
	srv := server(t)
	refused := httptest.NewServer(http.NotFoundHandler())
	refusedURL := "ws" + strings.TrimPrefix(refused.URL, "http")
	refused.Close()
	for _, tt := range []struct {
		name, url, wantCode, wantStatus string
		connecting                      int
	}{
		{"refused", refusedURL + "/echo", "dial", "0", 1},
		{"handshake rejected", wsURL(srv, "/nope"), "server", "404", 1},
		{"not a ws URL", "http://example.test/", "invalid", "0", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := protocoltest.New(t, ws.Module{})
			var called bool
			_ = h.VU().Runtime().Set("mark", func() { called = true })
			if _, err := h.Run(`var res = ws.connect("` + tt.url + `", function (socket) { mark(); });`); err != nil {
				t.Fatal(err)
			}
			res := h.VU().Runtime().Get("res").ToObject(h.VU().Runtime())
			if called {
				t.Error("the callback ran without a connection")
			}
			if str(res, "error_code") != tt.wantCode || str(res, "status") != tt.wantStatus || str(res, "error") == "" {
				t.Errorf("result: code %q status %s error %q", str(res, "error_code"), str(res, "status"), str(res, "error"))
			}
			if c := h.Family(ws.MetricConnecting); c.Count != tt.connecting || c.Failed != tt.connecting {
				t.Errorf("connecting = %d samples, %d failed", c.Count, c.Failed)
			}
			if s := h.Family(ws.MetricSessionFailed); s.Count != 1 || s.Trues != 1 {
				t.Errorf("session failed = %d of %d", s.Trues, s.Count)
			}
		})
	}
}

func TestMisuse(t *testing.T) {
	h := protocoltest.New(t, ws.Module{})
	for src, want := range map[string]string{
		`ws.connect()`:                "url is required",
		`ws.connect("ws://x", {}, 1)`: "the third argument must be a function",
		`ws.connect("WS", function (s) { s.on("data", function () {}); })`: "unknown event",
	} {
		_, err := h.Run(strings.ReplaceAll(src, "WS", wsURL(server(t), "/echo")))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", src, err, want)
		}
	}
	h.VU().SetContext(nil) // top-level code
	if _, err := h.Run(`ws.connect("ws://x", function () {})`); err == nil || !strings.Contains(err.Error(), "top-level code") {
		t.Errorf("top-level connect: %v", err)
	}
}

// A handler that throws ends the session, closes the socket, and connect
// rethrows the exception; no goroutine is left behind.
func TestHandlerThrows(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	h := protocoltest.New(t, ws.Module{})
	_, err := h.Run(strings.ReplaceAll(`ws.connect("WS/echo", function (socket) {
	socket.on("open", function () { socket.send("x"); });
	socket.on("message", function () { throw new Error("boom"); });
});`, "WS", wsURL(srv, "")))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want boom", err)
	}
	waitGoroutines(t, before)
}

// When the test ends during a session, connect returns promptly, records
// nothing for the session, and leaves no goroutine behind.
func TestCancellation(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	h := protocoltest.New(t, ws.Module{})
	ctx, cancel := context.WithCancel(context.Background())
	h.VU().SetContext(ctx)
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := h.Run(strings.ReplaceAll(`ws.connect("WS/silent", function (socket) {
	socket.on("open", function () { socket.send("x"); });
});`, "WS", wsURL(srv, "")))
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("connect took %v after cancellation", took)
	}
	_ = err // the run may report the interrupt; either is fine
	if s := h.Family(ws.MetricSessionFailed); s.Count != 0 {
		t.Errorf("a cancelled session was recorded: %+v", s)
	}
	if d := h.Family(ws.MetricSessionDuration); d.Count != 0 {
		t.Errorf("a cancelled session's duration was recorded")
	}
	waitGoroutines(t, before)
}

// The handshake sends the VU's cookies, so a login over HTTP carries over.
func TestHandshakeSendsCookies(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, ws.Module{})
	u := srv.URL + "/"
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	h.VU().HTTPClient().Jar.SetCookies(req.URL, []*http.Cookie{{Name: "session", Value: "abc"}})
	run(t, h, srv, `
var first = "";
var res = ws.connect("WS/cookie", function (socket) {
	socket.on("message", function (data) { first = data; socket.close(); });
});`)
	if got := h.VU().Runtime().Get("first").String(); got != "abc" {
		t.Errorf("server saw cookie %q, want abc", got)
	}
}

// Many VUs, each with its own runtime and instance, run sessions at once
// against one server (run with -race). Each VU's state is its own; the
// shared run only provides the transport and family IDs.
func TestConcurrentVUs(t *testing.T) {
	srv := server(t)
	before := runtime.NumGoroutine()
	const vus, sessions, msgs = 16, 5, 10
	h := protocoltest.New(t, ws.Module{}) // provides the run's families
	_ = h
	var wg sync.WaitGroup
	var failures atomic.Int64
	for range vus {
		wg.Go(func() {
			vh := protocoltest.New(t, ws.Module{})
			src := strings.ReplaceAll(`
for (var s = 0; s < SESSIONS; s++) {
	var n = 0;
	var res = ws.connect("WS/echo", function (socket) {
		socket.on("open", function () { for (var i = 0; i < MSGS; i++) socket.send("m" + i, { reply: true }); });
		socket.on("message", function () { if (++n === MSGS) socket.close(); });
	});
	if (res.error !== "" || n !== MSGS) throw new Error("session: " + res.error + ", " + n + " messages");
}`, "WS", wsURL(srv, ""))
			src = strings.NewReplacer("SESSIONS", "5", "MSGS", "10").Replace(src)
			if _, err := vh.Run(src); err != nil {
				failures.Add(1)
				t.Error(err)
				return
			}
			if got := vh.Family(ws.MetricMsgLatency).Count; got != sessions*msgs {
				t.Errorf("latency samples = %d, want %d", got, sessions*msgs)
			}
			if err := vh.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if failures.Load() > 0 {
		t.FailNow()
	}
	waitGoroutines(t, before+2) // the harness h and the server's idle state
}

// The module only depends on what ADR-018 allows: no scenario, runner or
// command-line packages.
func TestModuleSatisfiesInterface(t *testing.T) {
	var _ protocol.Module = ws.Module{}
	m := ws.Module{}
	if m.Name() != "ws" || len(m.Exports()) != 1 || m.Exports()[0] != "connect" || len(m.Metrics()) != 8 {
		t.Errorf("module: %s %v %d metrics", m.Name(), m.Exports(), len(m.Metrics()))
	}
}

// BenchmarkSessionsParallel runs whole sessions (connect, 10 echoed
// messages timed with reply, close) from many VUs at once against a local
// echo server; one op is one session. Each goroutine is one VU with its
// own runtime and instance, as in a run.
func BenchmarkSessionsParallel(b *testing.B) {
	srv := server(b)
	src := strings.ReplaceAll(`
var n = 0;
var res = ws.connect("WS/echo", function (socket) {
	socket.on("open", function () { for (var i = 0; i < 10; i++) socket.send("m", { reply: true }); });
	socket.on("message", function () { if (++n === 10) socket.close(); });
});
if (res.error !== "" || n !== 10) throw new Error(res.error);`, "WS", wsURL(srv, ""))
	b.ReportAllocs()
	b.SetParallelism(4) // 4 x GOMAXPROCS VUs
	b.RunParallel(func(pb *testing.PB) {
		h := protocoltest.New(b, ws.Module{})
		prog, err := goja.Compile("bench.js", src, false)
		if err != nil {
			b.Error(err)
			return
		}
		for pb.Next() {
			if _, err := h.VU().Runtime().RunProgram(prog); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
