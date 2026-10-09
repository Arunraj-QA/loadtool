package ws

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// defaultReceiveTimeout is how long receive waits when the script gives
// no timeout.
const defaultReceiveTimeout = 30 * time.Second

// blockingSocket is a session in the blocking style (ADR-019 amendment):
//
//	const socket = ws.connect(url);
//	socket.send("hello");
//	const reply = socket.receive(5000); // the message, or null
//	socket.close();
//
// It reuses the callback style's session for the handshake, the reader
// goroutine, error mapping and recording; instead of an event loop, the
// script pulls messages with receive. It belongs to the iteration (or
// setup, or teardown) that opened it: one left open is closed when that
// returns (instance.EndIteration).
type blockingSocket struct {
	s           *session
	obj         *goja.Object
	events      chan event
	stopReading context.CancelFunc
	finished    bool
}

// connectBlocking implements ws.connect(url, params?) without a callback.
func (i *instance) connectBlocking(s *session, params goja.Value) goja.Value {
	b := &blockingSocket{s: s, obj: s.rt.NewObject()}
	if s.dial(headersFrom(s.rt, params)) {
		b.events = make(chan event, 16)
		var readCtx context.Context
		readCtx, b.stopReading = context.WithCancel(context.Background())
		go read(readCtx, s.conn, b.events)
		i.open = append(i.open, b)
	} else {
		b.finished = true // nothing to release
	}
	rt := s.rt
	_ = b.obj.Set("send", func(call goja.FunctionCall) goja.Value {
		data := call.Argument(0)
		if !isSet(data) {
			panic(rt.NewTypeError("socket.send: a string is required"))
		}
		return rt.ToValue(b.send(websocket.MessageText, []byte(data.String()), call.Argument(1)))
	})
	_ = b.obj.Set("sendBinary", func(call goja.FunctionCall) goja.Value {
		buf, ok := call.Argument(0).Export().(goja.ArrayBuffer)
		if !ok {
			panic(rt.NewTypeError("socket.sendBinary: an ArrayBuffer is required"))
		}
		return rt.ToValue(b.send(websocket.MessageBinary, buf.Bytes(), call.Argument(1)))
	})
	_ = b.obj.Set("receive", func(call goja.FunctionCall) goja.Value {
		timeout := defaultReceiveTimeout
		if arg := call.Argument(0); isSet(arg) {
			ms := arg.ToFloat()
			if ms < 0 || ms != ms {
				panic(rt.NewTypeError("socket.receive: the timeout must be a number of milliseconds, 0 or more"))
			}
			timeout = time.Duration(ms * float64(time.Millisecond))
		}
		return b.receive(timeout)
	})
	_ = b.obj.Set("close", func(call goja.FunctionCall) goja.Value {
		code := int64(websocket.StatusNormalClosure)
		if arg := call.Argument(0); isSet(arg) {
			code = arg.ToInteger()
		}
		b.close(websocket.StatusCode(code))
		return goja.Undefined()
	})
	b.update()
	return b.obj
}

// update copies the session's state to the socket's fields: status, url,
// error, error_code, closed and timings, as connect's result has them.
func (b *blockingSocket) update() {
	s := b.s
	msg := ""
	if s.err != nil {
		msg = s.err.Error()
	}
	_ = b.obj.Set("url", s.url)
	_ = b.obj.Set("status", s.status)
	_ = b.obj.Set("error", msg)
	_ = b.obj.Set("error_code", string(s.code))
	_ = b.obj.Set("closed", b.finished || s.done)
	timings := s.rt.NewObject()
	_ = timings.Set("connecting", float64(s.connecting)/float64(time.Millisecond))
	_ = timings.Set("duration", float64(s.duration)/float64(time.Millisecond))
	_ = b.obj.Set("timings", timings)
}

// send writes one message. In this style every send is timed until a
// later receive returns a message, unless { reply: false } is given.
func (b *blockingSocket) send(typ websocket.MessageType, payload []byte, opts goja.Value) bool {
	reply := true
	if isSet(opts) {
		if r := opts.ToObject(b.s.rt).Get("reply"); isSet(r) {
			reply = r.ToBoolean()
		}
	}
	defer b.update()
	if b.finished {
		b.s.add(b.s.inst.run.ids.errors, 1)
		b.s.err, b.s.code = errSocketClosed, protocol.CodeClosed
		return false
	}
	ok := b.s.send(typ, payload, reply)
	if b.s.done {
		b.finish()
	}
	return ok
}

// receive returns the next message (a string, or an ArrayBuffer for a
// binary one), or null when none arrives within timeout, the socket
// closes, or the test ends. A timeout counts in ws_errors and sets
// error_code "timeout", but the socket stays open.
func (b *blockingSocket) receive(timeout time.Duration) goja.Value {
	s := b.s
	defer b.update()
	if b.finished || s.done {
		return goja.Null()
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case ev, ok := <-b.events:
		if !ok {
			s.end(nil, int(websocket.StatusAbnormalClosure))
			b.finish()
			return goja.Null()
		}
		if ev.err != nil {
			s.readFailed(ev.err)
			b.finish()
			return goja.Null()
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
		if ev.typ == websocket.MessageBinary {
			return s.rt.ToValue(s.rt.NewArrayBuffer(ev.data))
		}
		return s.rt.ToValue(string(ev.data))
	case <-t.C:
		s.add(s.inst.run.ids.errors, 1)
		s.err, s.code = errReceiveTimeout(timeout), protocol.CodeTimeout
		return goja.Null()
	case <-s.ctx.Done():
		return goja.Null() // the test is ending: nothing is recorded
	}
}

// close closes the socket and waits until it is closed. Messages still
// arriving are discarded, as the close handshake does.
func (b *blockingSocket) close(code websocket.StatusCode) {
	defer b.update()
	if b.finished {
		return
	}
	s := b.s
	if !s.done {
		s.closeSocket(code)
		// Drain until the reader stops: the peer's close frame (or a
		// failure) ends the session.
		for ev := range b.events {
			if ev.err != nil {
				s.readFailed(ev.err)
				break
			}
		}
		s.end(nil, int(code)) // no-op if readFailed ended it
	}
	b.finish()
}

// finish releases the socket: it stops the reader and waits for it, waits
// for the close handshake, and records the session's end (unless the test
// is ending). It is idempotent.
func (b *blockingSocket) finish() {
	if b.finished {
		return
	}
	b.finished = true
	s := b.s
	b.stopReading()
	s.conn.CloseNow()
	for range b.events {
	}
	s.closer.Wait()
	if !s.done {
		s.end(nil, int(websocket.StatusNormalClosure))
	}
	if s.ctx.Err() == nil {
		s.recordSessionEnd()
	}
	b.update()
}

func errReceiveTimeout(d time.Duration) error {
	return &receiveTimeoutError{d}
}

type receiveTimeoutError struct{ d time.Duration }

func (e *receiveTimeoutError) Error() string {
	return "no message within " + e.d.String()
}
