package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

func isSet(v goja.Value) bool {
	return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v)
}

// jsonFuncs returns the runtime's JSON.stringify and JSON.parse.
func (i *instance) jsonFuncs() (goja.Callable, goja.Callable) {
	if i.stringify == nil {
		rt := i.vu.Runtime()
		j := rt.Get("JSON").ToObject(rt)
		i.stringify, _ = goja.AssertFunction(j.Get("stringify"))
		i.parse, _ = goja.AssertFunction(j.Get("parse"))
	}
	return i.stringify, i.parse
}

// toMessage converts a JavaScript object to a message of type desc, by
// way of JSON (protojson's mapping: lowerCamelCase or original field
// names, enums by name or number).
func (i *instance) toMessage(v goja.Value, desc protoreflect.MessageDescriptor) (*dynamicpb.Message, error) {
	msg := dynamicpb.NewMessage(desc)
	if !isSet(v) {
		return msg, nil
	}
	stringify, _ := i.jsonFuncs()
	s, err := stringify(goja.Undefined(), v)
	if err != nil {
		return nil, fmt.Errorf("the request is not JSON-serializable: %w", err)
	}
	if err := protojson.Unmarshal([]byte(s.String()), msg); err != nil {
		return nil, fmt.Errorf("the request does not fit %s: %w", desc.FullName(), err)
	}
	return msg, nil
}

// marshalOptions write every field, defaults included, so a script sees
// index: 0 rather than a missing field.
var marshalOptions = protojson.MarshalOptions{EmitUnpopulated: true}

// toValue converts a message to a JavaScript object.
func (i *instance) toValue(msg *dynamicpb.Message) goja.Value {
	b, err := marshalOptions.Marshal(msg)
	if err != nil {
		panic(i.vu.Runtime().NewGoError(err))
	}
	_, parse := i.jsonFuncs()
	v, err := parse(goja.Undefined(), i.vu.Runtime().ToValue(string(b)))
	if err != nil {
		panic(err)
	}
	return v
}

// callParams are the params of invoke and stream.
type callParams struct {
	md      metadata.MD
	timeout time.Duration
}

// readParams reads { metadata: { key: value }, timeout: ms or "2s" }.
func readParams(rt *goja.Runtime, v goja.Value) callParams {
	p := callParams{timeout: defaultTimeout}
	if !isSet(v) {
		return p
	}
	obj := v.ToObject(rt)
	if mv := obj.Get("metadata"); isSet(mv) {
		m := mv.ToObject(rt)
		p.md = metadata.MD{}
		for _, k := range m.Keys() {
			p.md.Append(strings.ToLower(k), m.Get(k).String())
		}
	}
	if tv := obj.Get("timeout"); isSet(tv) {
		p.timeout = parseTimeout(rt, tv)
	}
	return p
}

// parseTimeout reads a timeout: a number of milliseconds, or a duration
// string such as "2s".
func parseTimeout(rt *goja.Runtime, v goja.Value) time.Duration {
	if s, ok := v.Export().(string); ok {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			panic(rt.NewTypeError("grpc: timeout %q must be a positive duration, such as \"2s\"", s))
		}
		return d
	}
	ms := v.ToFloat()
	if ms <= 0 || ms != ms {
		panic(rt.NewTypeError("grpc: timeout must be a positive number of milliseconds or a duration such as \"2s\""))
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// mdValue converts metadata to { key: "v1, v2" }, as HTTP headers are.
func mdValue(rt *goja.Runtime, md metadata.MD) goja.Value {
	o := rt.NewObject()
	for k, vs := range md {
		_ = o.Set(k, strings.Join(vs, ", "))
	}
	return o
}

// errorCode maps a call's error to an error_code (ADR-020).
func errorCode(err error) protocol.ErrorCode {
	if err == nil {
		return protocol.CodeNone
	}
	st, ok := status.FromError(err)
	if !ok {
		if errors.Is(err, context.DeadlineExceeded) {
			return protocol.CodeTimeout
		}
		return protocol.Classify(err)
	}
	switch st.Code() {
	case codes.OK:
		return protocol.CodeNone
	case codes.DeadlineExceeded:
		return protocol.CodeTimeout
	case codes.Canceled:
		return protocol.CodeClosed
	case codes.Unavailable:
		// grpc-go keeps the transport error only as text.
		msg := st.Message()
		switch {
		case strings.Contains(msg, "x509") || strings.Contains(msg, "tls:") || strings.Contains(msg, "certificate"):
			return protocol.CodeTLS
		case strings.Contains(msg, "no such host"):
			return protocol.CodeDNS
		case strings.Contains(msg, "connection refused") || strings.Contains(msg, "dial tcp") || strings.Contains(msg, "actively refused"):
			return protocol.CodeDial
		}
		return protocol.CodeClosed
	}
	return protocol.CodeServer
}

// setStatus sets status, status_text, error and error_code on o.
func setStatus(o *goja.Object, err error, code protocol.ErrorCode) {
	st := status.Convert(err)
	_ = o.Set("status", int(st.Code()))
	_ = o.Set("status_text", st.Code().String())
	msg := ""
	if err != nil {
		msg = st.Message()
	}
	_ = o.Set("error", msg)
	_ = o.Set("error_code", string(code))
}
