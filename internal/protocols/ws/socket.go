package ws

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// events are the names socket.on accepts.
var eventNames = []string{"open", "message", "close", "error"}

var errSocketClosed = errors.New("the socket is closed")

// socketObject builds the object the setup callback receives:
//
//	on(event, fn)           "open", "message" (data), "close" (code), "error" ({ error, error_code })
//	send(text, { reply })   send a text message; reply: true times the next message received
//	sendBinary(buf, { reply })  send an ArrayBuffer
//	close(code?)            start the close handshake (default 1000)
//	setTimeout(fn, ms), setInterval(fn, ms)  timers that run inside the session
func (s *session) socketObject() *goja.Object {
	rt := s.rt
	o := rt.NewObject()
	_ = o.Set("on", func(name string, fn goja.Value) {
		cb, ok := goja.AssertFunction(fn)
		if !slices.Contains(eventNames, name) {
			panic(rt.NewTypeError("socket.on: unknown event %q; events are open, message, close and error", name))
		}
		if !ok {
			panic(rt.NewTypeError("socket.on(%q): the handler must be a function", name))
		}
		s.handlers[name] = append(s.handlers[name], cb)
	})
	_ = o.Set("send", func(call goja.FunctionCall) goja.Value {
		data := call.Argument(0)
		if !isSet(data) {
			panic(rt.NewTypeError("socket.send: a string is required"))
		}
		return rt.ToValue(s.send(websocket.MessageText, []byte(data.String()), replyOption(rt, call.Argument(1))))
	})
	_ = o.Set("sendBinary", func(call goja.FunctionCall) goja.Value {
		buf, ok := call.Argument(0).Export().(goja.ArrayBuffer)
		if !ok {
			panic(rt.NewTypeError("socket.sendBinary: an ArrayBuffer is required"))
		}
		return rt.ToValue(s.send(websocket.MessageBinary, buf.Bytes(), replyOption(rt, call.Argument(1))))
	})
	_ = o.Set("close", func(call goja.FunctionCall) goja.Value {
		code := int64(websocket.StatusNormalClosure)
		if arg := call.Argument(0); isSet(arg) {
			code = arg.ToInteger()
		}
		s.closeSocket(websocket.StatusCode(code))
		return goja.Undefined()
	})
	_ = o.Set("setTimeout", func(fn goja.Value, ms float64) int { return s.addTimer(fn, ms, false) })
	_ = o.Set("setInterval", func(fn goja.Value, ms float64) int { return s.addTimer(fn, ms, true) })
	return o
}

func replyOption(rt *goja.Runtime, v goja.Value) bool {
	if !isSet(v) {
		return false
	}
	r := v.ToObject(rt).Get("reply")
	return r != nil && r.ToBoolean()
}

// send writes one message. A failure fires "error", counts in ws_errors
// and ends the session; send returns whether the message was written.
func (s *session) send(typ websocket.MessageType, payload []byte, reply bool) bool {
	ids := s.inst.run.ids
	if s.done || s.closing {
		s.add(ids.errors, 1)
		s.emitError(errSocketClosed, protocol.CodeClosed)
		return false
	}
	ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
	start := time.Now()
	err := s.conn.Write(ctx, typ, payload)
	cancel()
	if err != nil {
		if s.ctx.Err() != nil {
			s.done = true // the test is ending
			return false
		}
		s.add(ids.errors, 1)
		code := protocol.Classify(err)
		s.emitError(err, code)
		s.end(err, int(websocket.StatusAbnormalClosure))
		s.code = code
		return false
	}
	s.add(ids.msgsSent, 1)
	if reply {
		if len(s.pending) >= maxPendingReplies {
			s.pending = s.pending[1:]
			s.inst.run.warn("ws: more than 1024 sends are awaiting a reply in one session; the oldest are no longer timed")
		}
		s.pending = append(s.pending, start)
	}
	return true
}

// closeSocket starts the close handshake. It runs on a goroutine of its
// own, joined before connect returns, because it waits for the peer's
// close frame, which the reader goroutine must be free to deliver.
func (s *session) closeSocket(code websocket.StatusCode) {
	if s.closing || s.done {
		return
	}
	s.closing = true
	conn := s.conn
	s.closer.Go(func() { _ = conn.Close(code, "") })
}

func (s *session) addTimer(fn goja.Value, ms float64, repeat bool) int {
	cb, ok := goja.AssertFunction(fn)
	if !ok {
		panic(s.rt.NewTypeError("socket timers need a function"))
	}
	if ms < 0 || ms != ms {
		ms = 0
	}
	d := time.Duration(ms * float64(time.Millisecond))
	if repeat && d <= 0 {
		panic(s.rt.NewTypeError("socket.setInterval: the interval must be above 0 ms"))
	}
	s.nextID++
	t := &timer{id: s.nextID, at: time.Now().Add(d), fn: cb}
	if repeat {
		t.every = d
	}
	s.timers = append(s.timers, t)
	return t.id
}

// nextTimer returns when the earliest timer is due.
func (s *session) nextTimer() (time.Time, bool) {
	var next time.Time
	for _, t := range s.timers {
		if next.IsZero() || t.at.Before(next) {
			next = t.at
		}
	}
	return next, !next.IsZero()
}

// fireTimers runs every due timer, earliest first.
func (s *session) fireTimers() {
	now := time.Now()
	due := slices.DeleteFunc(slices.Clone(s.timers), func(t *timer) bool { return t.at.After(now) })
	slices.SortStableFunc(due, func(a, b *timer) int { return a.at.Compare(b.at) })
	for _, t := range due {
		if s.done {
			return
		}
		if t.every > 0 {
			t.at = t.at.Add(t.every)
			if t.at.Before(now) {
				t.at = now.Add(t.every) // fell behind: do not fire in a burst
			}
		} else {
			s.timers = slices.DeleteFunc(s.timers, func(x *timer) bool { return x == t })
		}
		if _, err := t.fn(goja.Undefined()); err != nil {
			s.handleThrow(err)
			return
		}
	}
}

func isSet(v goja.Value) bool {
	return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v)
}

// headersFrom reads params.headers.
func headersFrom(rt *goja.Runtime, params goja.Value) http.Header {
	if !isSet(params) {
		return nil
	}
	hv := params.ToObject(rt).Get("headers")
	if !isSet(hv) {
		return nil
	}
	obj := hv.ToObject(rt)
	h := make(http.Header, len(obj.Keys()))
	for _, k := range obj.Keys() {
		h.Set(k, obj.Get(k).String())
	}
	return h
}
