package ws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

var errInitConnect = errors.New("ws.connect is not allowed in the script's top-level code; call it inside the default function")

// connect implements ws.connect(url, params?, callback). It returns the
// session's result after the socket closed. A handshake failure is in the
// result (the callback is not called); script misuse throws.
func (i *instance) connect(call goja.FunctionCall) goja.Value {
	rt := i.vu.Runtime()
	ctx := i.vu.Context()
	if ctx == nil {
		panic(rt.NewGoError(errInitConnect))
	}
	rawURL := call.Argument(0)
	if !isSet(rawURL) {
		panic(rt.NewTypeError("ws.connect: url is required"))
	}
	params, cbArg := call.Argument(1), call.Argument(2)
	if !isSet(cbArg) {
		params, cbArg = goja.Undefined(), params // connect(url, callback)
	}
	cb, ok := goja.AssertFunction(cbArg)
	if !ok {
		panic(rt.NewTypeError("ws.connect: the last argument must be a function that sets up the socket"))
	}
	header := headersFrom(rt, params)

	s := &session{inst: i, rt: rt, ctx: ctx, url: rawURL.String()}
	if !s.dial(header) {
		return s.result()
	}
	s.run(cb)
	return s.result()
}

// session is one connect call. It is used only on the VU's goroutine,
// except where noted.
type session struct {
	inst *instance
	rt   *goja.Runtime
	ctx  context.Context
	url  string

	conn       *websocket.Conn
	status     int
	connecting time.Duration
	opened     time.Time
	duration   time.Duration
	err        error
	code       protocol.ErrorCode
	closeCode  int

	handlers map[string][]goja.Callable
	timers   []*timer
	nextID   int
	pending  []time.Time // send times awaiting a reply, oldest first
	closing  bool        // close() was called
	done     bool
	thrown   error // an exception from a handler, rethrown by connect
	// closer runs the close handshake off the VU's goroutine (see close).
	closer sync.WaitGroup
}

type timer struct {
	id    int
	at    time.Time
	every time.Duration // 0 for setTimeout
	fn    goja.Callable
}

// event is what the reader goroutine passes to the VU's goroutine.
type event struct {
	typ  websocket.MessageType
	data []byte
	err  error
	at   time.Time // when Read returned, for message latency
}

// dial performs the handshake and records it. It reports whether a
// session was established.
func (s *session) dial(header http.Header) bool {
	ids := s.inst.run.ids
	f := protocol.Families{Duration: ids.connecting, Count: ids.sessions, Failed: protocol.NoFamily}
	if u, err := url.Parse(s.url); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		if err == nil {
			err = fmt.Errorf("ws.connect: %q is not a ws:// or wss:// URL", s.url)
		}
		s.fail(protocol.Record(s.inst.vu, f, protocol.Outcome{Err: err}), err)
		s.recordSessionFailed(true)
		return false
	}

	client := &http.Client{Transport: s.inst.run.transport}
	if jar := s.inst.vu.HTTPClient(); jar != nil {
		client.Jar = jar.Jar // the VU's cookies, as for HTTP requests
	}
	dialCtx, cancel := context.WithTimeout(s.ctx, connectTimeout)
	defer cancel()
	start := time.Now()
	conn, resp, err := websocket.Dial(dialCtx, s.url, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	s.connecting = time.Since(start)
	if resp != nil {
		s.status = resp.StatusCode
	}
	out := protocol.Outcome{Duration: s.connecting, Err: err, Sent: true}
	if err != nil && s.status >= 400 {
		out.Code = protocol.CodeServer // the server refused the upgrade
	} else if err != nil && s.status != 0 {
		out.Code = protocol.CodeProtocol
	}
	if err != nil {
		s.fail(protocol.Record(s.inst.vu, f, out), err)
		s.recordSessionFailed(true)
		return false
	}
	protocol.Record(s.inst.vu, f, out)
	conn.SetReadLimit(readLimit)
	s.conn, s.opened = conn, time.Now()
	return true
}

// run calls the setup callback, fires "open" and runs the session's
// event loop until the socket closes, then releases everything.
func (s *session) run(cb goja.Callable) {
	events := make(chan event, 16)
	readCtx, stopReading := context.WithCancel(context.Background())
	go read(readCtx, s.conn, events)
	defer func() {
		// Release in order: stop the reader and wait for it, then for the
		// close handshake, so no goroutine outlives connect.
		stopReading()
		s.conn.CloseNow()
		for range events {
		}
		s.closer.Wait()
		if s.thrown != nil {
			panic(s.thrown)
		}
	}()

	s.handlers = make(map[string][]goja.Callable)
	if _, err := cb(goja.Undefined(), s.socketObject()); err != nil {
		s.handleThrow(err)
		return
	}
	s.emit("open")

	wake := time.NewTimer(time.Hour)
	defer wake.Stop()
	for !s.done {
		var timerC <-chan time.Time
		if next, ok := s.nextTimer(); ok {
			wake.Reset(time.Until(next))
			timerC = wake.C
		}
		select {
		case ev, ok := <-events:
			if !ok {
				s.end(nil, 1006)
				break
			}
			s.handle(ev)
		case <-timerC:
			s.fireTimers()
		case <-s.ctx.Done():
			// The test is ending: close at once, record nothing.
			s.done = true
		}
	}
	if s.ctx.Err() == nil {
		s.recordSessionEnd()
	}
}

// read is the session's reader goroutine. It never touches goja or a
// recorder; it ends when the connection fails or closes, or when the
// session stops it, and closes events when it does.
func read(ctx context.Context, conn *websocket.Conn, events chan<- event) {
	defer close(events)
	for {
		typ, data, err := conn.Read(ctx)
		ev := event{typ: typ, data: data, err: err, at: time.Now()}
		select {
		case events <- ev:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// handle processes one event from the reader on the VU's goroutine.
func (s *session) handle(ev event) {
	if ev.err != nil {
		s.readFailed(ev.err)
		return
	}
	ids := s.inst.run.ids
	s.add(ids.msgsReceived, 1)
	if len(s.pending) > 0 {
		sent := s.pending[0]
		s.pending = s.pending[1:]
		if rec := s.recorder(); rec != nil {
			rec.Trend(ids.msgLatency, ev.at.Sub(sent), true)
		}
	}
	if len(s.handlers["message"]) == 0 {
		return // no handler: the data is never converted
	}
	var data goja.Value
	if ev.typ == websocket.MessageBinary {
		data = s.rt.ToValue(s.rt.NewArrayBuffer(ev.data))
	} else {
		data = s.rt.ToValue(string(ev.data))
	}
	s.emit("message", data)
}

// readFailed ends the session after the reader stopped with err: a close
// frame from the peer, our own close completing, or a receive error.
func (s *session) readFailed(err error) {
	code := int(websocket.CloseStatus(err))
	switch {
	case code == int(websocket.StatusNormalClosure) || code == int(websocket.StatusGoingAway):
		s.end(nil, code)
	case code >= 0:
		s.end(fmt.Errorf("closed with status %d: %w", code, err), code)
		s.code = protocol.CodeServer
	case s.closing:
		s.end(nil, int(websocket.StatusNormalClosure)) // our close completed
	default:
		s.add(s.inst.run.ids.errors, 1)
		c := protocol.Classify(err)
		s.emitError(err, c)
		s.end(err, int(websocket.StatusAbnormalClosure))
		s.code = c
	}
}

// end marks the session over with err (nil for a clean close) and fires
// "close" with the close code.
func (s *session) end(err error, closeCode int) {
	if s.done {
		return
	}
	s.done, s.duration, s.closeCode = true, time.Since(s.opened), closeCode
	if err != nil && s.err == nil {
		s.err = err
		if s.code == protocol.CodeNone {
			s.code = protocol.Classify(err)
		}
	}
	s.emit("close", s.rt.ToValue(closeCode))
}

func (s *session) recordSessionEnd() {
	failed := s.err != nil
	if rec := s.recorder(); rec != nil {
		rec.Trend(s.inst.run.ids.sessionDuration, s.duration, !failed)
	}
	s.recordSessionFailed(failed)
}

func (s *session) recordSessionFailed(failed bool) {
	if rec := s.recorder(); rec != nil {
		rec.Rate(s.inst.run.ids.sessionFailed, failed)
	}
}

// recorder returns the VU's recorder, or nil once the test is ending, so
// nothing cut short by the end of the test is recorded.
func (s *session) recorder() *metrics.Recorder {
	if s.ctx.Err() != nil {
		return nil
	}
	return s.inst.vu.Recorder()
}

func (s *session) add(id metrics.FamilyID, n int64) {
	if rec := s.recorder(); rec != nil {
		rec.Add(id, n)
	}
}

func (s *session) fail(code protocol.ErrorCode, err error) {
	s.err, s.code = err, code
}

// result is connect's return value.
func (s *session) result() goja.Value {
	o := s.rt.NewObject()
	_ = o.Set("url", s.url)
	_ = o.Set("status", s.status)
	msg := ""
	if s.err != nil {
		msg = s.err.Error()
	}
	_ = o.Set("error", msg)
	_ = o.Set("error_code", string(s.code))
	timings := s.rt.NewObject()
	_ = timings.Set("connecting", float64(s.connecting)/float64(time.Millisecond))
	_ = timings.Set("duration", float64(s.duration)/float64(time.Millisecond))
	_ = o.Set("timings", timings)
	return o
}

// emit calls the handlers registered for name. A handler that throws ends
// the session; its exception is rethrown by connect.
func (s *session) emit(name string, args ...goja.Value) {
	for _, fn := range s.handlers[name] {
		if s.thrown != nil {
			return
		}
		if _, err := fn(goja.Undefined(), args...); err != nil {
			s.handleThrow(err)
			return
		}
	}
}

func (s *session) emitError(err error, code protocol.ErrorCode) {
	if len(s.handlers["error"]) == 0 {
		return
	}
	e := s.rt.NewObject()
	_ = e.Set("error", err.Error())
	_ = e.Set("error_code", string(code))
	s.emit("error", e)
}

// handleThrow ends the session after a handler threw (or was interrupted
// because the test is ending).
func (s *session) handleThrow(err error) {
	s.done = true
	if s.ctx.Err() != nil {
		return // interrupted: the iteration ends anyway
	}
	s.thrown = err
	if ex, ok := err.(*goja.Exception); ok {
		s.thrown = ex
	}
}
