package grpc

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/dop251/goja"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// stream is one streaming call, of any kind, in the blocking style
// (ADR-020): the script sends, closes its side and receives, each call
// blocking on the VU's goroutine within the stream's context. It starts
// no goroutines.
type stream struct {
	inst   *instance
	obj    *goja.Object
	cs     grpc.ClientStream
	out    protoreflect.MessageDescriptor
	in     protoreflect.MessageDescriptor
	cancel context.CancelFunc
	start  time.Time
	done   bool
}

// stream(method, { metadata, timeout }) opens a stream for a streaming
// method. The timeout bounds the whole stream.
func (c *client) stream(call goja.FunctionCall) goja.Value {
	c.requireIteration("client.stream")
	i := c.inst
	rt := i.vu.Runtime()
	method := call.Argument(0)
	if !isSet(method) {
		panic(rt.NewTypeError("client.stream: a method such as \"greeter.Greeter/Chat\" is required"))
	}
	params := readParams(rt, call.Argument(1))
	s := &stream{inst: i, obj: rt.NewObject()}
	s.methods()
	end := func(err error) goja.Value { // the stream never opened
		s.done = true
		code := protocol.Record(i.vu, i.run.stream, protocol.Outcome{Err: err})
		setStatus(s.obj, err, code)
		_ = s.obj.Set("closed", true)
		return s.obj
	}
	if c.conn == nil {
		return end(errNotConnected)
	}
	md, fullMethod, err := findMethod(c.sets, method.String())
	if err != nil {
		return end(err)
	}
	if !md.IsStreamingClient() && !md.IsStreamingServer() {
		panic(rt.NewTypeError("client.stream: %s is a unary method; use client.invoke", fullMethod))
	}
	s.out, s.in = md.Input(), md.Output()

	ctx, cancel := context.WithTimeout(i.vu.Context(), params.timeout)
	if params.md != nil {
		ctx = metadata.NewOutgoingContext(ctx, params.md)
	}
	s.cancel, s.start = cancel, time.Now()
	desc := &grpc.StreamDesc{StreamName: string(md.Name()), ServerStreams: md.IsStreamingServer(), ClientStreams: md.IsStreamingClient()}
	if rec := i.vu.Recorder(); rec != nil && i.vu.Context().Err() == nil {
		rec.Add(i.run.streams, 1)
	}
	s.cs, err = c.conn.NewStream(ctx, desc, fullMethod)
	if err != nil {
		s.finish(err)
		return s.obj
	}
	i.open = append(i.open, s)
	setStatus(s.obj, nil, protocol.CodeNone)
	_ = s.obj.Set("closed", false)
	return s.obj
}

func (s *stream) methods() {
	_ = s.obj.Set("send", func(msg goja.Value) bool { return s.send(msg) })
	_ = s.obj.Set("closeSend", func() { s.closeSend() })
	_ = s.obj.Set("recv", func() goja.Value { return s.recv() })
	_ = s.obj.Set("close", func() { s.close() })
}

// send sends one message; false once the stream is over. A request that
// does not fit the method's input type throws.
func (s *stream) send(v goja.Value) bool {
	if s.done {
		return false
	}
	msg, err := s.inst.toMessage(v, s.out)
	if err != nil {
		panic(s.inst.vu.Runtime().NewTypeError("stream.send: %s", err))
	}
	if err := s.cs.SendMsg(msg); err != nil {
		// io.EOF: the server ended the stream; recv reports its status.
		if !errors.Is(err, io.EOF) {
			s.finish(err)
		}
		return false
	}
	s.add(s.inst.run.sent)
	return true
}

func (s *stream) closeSend() {
	if !s.done {
		_ = s.cs.CloseSend()
	}
}

// recv returns the next message, or null once the stream ended; the
// stream's status fields then hold the final status.
func (s *stream) recv() goja.Value {
	if s.done {
		return goja.Null()
	}
	msg := dynamicpb.NewMessage(s.in)
	err := s.cs.RecvMsg(msg)
	if errors.Is(err, io.EOF) {
		s.finish(nil)
		return goja.Null()
	}
	if err != nil {
		s.finish(err)
		return goja.Null()
	}
	s.add(s.inst.run.received)
	return s.inst.toValue(msg)
}

// close cancels the stream if it is still open. Closing early is a normal
// end, not a failure: the script decided it had read enough.
func (s *stream) close() {
	if s.done {
		return
	}
	s.cancel()
	s.finish(nil)
	setStatus(s.obj, status.Error(codes.Canceled, "closed by the script"), protocol.CodeNone)
}

// finish records the stream's end with its final status (err nil is OK).
func (s *stream) finish(err error) {
	if s.done {
		return
	}
	s.done = true
	d := time.Since(s.start)
	code := errorCode(err)
	protocol.Record(s.inst.vu, s.inst.run.stream, protocol.Outcome{Duration: d, Err: err, Code: code, Sent: true})
	if s.cancel != nil {
		s.cancel()
	}
	setStatus(s.obj, err, code)
	if s.cs != nil {
		_ = s.obj.Set("trailers", mdValue(s.inst.vu.Runtime(), s.cs.Trailer()))
	}
	_ = s.obj.Set("closed", true)
	timings := s.inst.vu.Runtime().NewObject()
	_ = timings.Set("duration", float64(d)/float64(time.Millisecond))
	_ = s.obj.Set("timings", timings)
}

// add counts one stream message, unless the test is ending.
func (s *stream) add(id metrics.FamilyID) {
	vu := s.inst.vu
	if rec := vu.Recorder(); rec != nil && vu.Context().Err() == nil {
		rec.Add(id, 1)
	}
}
