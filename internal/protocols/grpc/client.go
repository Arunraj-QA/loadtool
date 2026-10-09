package grpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

var errNotConnected = errors.New("not connected: call client.connect first")

// client is one grpc.Client in one VU. Its connection is kept across
// iterations; the VU closes it at the end of the run (VU.OnClose).
type client struct {
	inst *instance
	obj  *goja.Object
	sets []*protoregistry.Files
	conn *grpc.ClientConn
}

// newClient builds the client object on this, the object new created.
func (i *instance) newClient(this *goja.Object) *goja.Object {
	c := &client{inst: i, obj: this}
	i.vu.OnClose(c)
	_ = this.Set("load", c.load)
	_ = this.Set("connect", c.connect)
	_ = this.Set("invoke", c.invoke)
	_ = this.Set("stream", c.stream)
	_ = this.Set("close", func() { _ = c.Close() })
	return nil // use this
}

// Close closes the connection. It is idempotent.
func (c *client) Close() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *client) requireIteration(what string) {
	if c.inst.vu.Context() == nil {
		panic(c.inst.vu.Runtime().NewGoError(fmt.Errorf("grpc: %s is not allowed in the script's top-level code; call it inside the default function", what)))
	}
}

// load(importPaths, ...files) parses .proto files. It is allowed in
// top-level code; a parse error throws.
func (c *client) load(call goja.FunctionCall) goja.Value {
	rt := c.inst.vu.Runtime()
	var paths []string
	if v := call.Argument(0); isSet(v) {
		if err := rt.ExportTo(v, &paths); err != nil {
			panic(rt.NewTypeError("client.load: the first argument must be a list of import paths, such as [\"proto\"]"))
		}
	}
	var files []string
	for _, a := range call.Arguments[1:] {
		files = append(files, a.String())
	}
	if len(files) == 0 {
		panic(rt.NewTypeError("client.load: name at least one .proto file"))
	}
	fs, err := c.inst.run.load(paths, files)
	if err != nil {
		panic(rt.NewGoError(fmt.Errorf("client.load: %w", err)))
	}
	c.sets = append(c.sets, fs)
	return goja.Undefined()
}

// connect(address, { plaintext, reflect, timeout }) dials and waits until
// the connection is ready. It returns { error, error_code }; a network
// failure does not throw.
func (c *client) connect(call goja.FunctionCall) goja.Value {
	c.requireIteration("client.connect")
	rt := c.inst.vu.Runtime()
	address := call.Argument(0)
	if !isSet(address) {
		panic(rt.NewTypeError("client.connect: an address such as \"127.0.0.1:8091\" is required"))
	}
	var plaintext, reflect bool
	timeout := defaultTimeout
	if p := call.Argument(1); isSet(p) {
		o := p.ToObject(rt)
		plaintext = isSet(o.Get("plaintext")) && o.Get("plaintext").ToBoolean()
		reflect = isSet(o.Get("reflect")) && o.Get("reflect").ToBoolean()
		if t := o.Get("timeout"); isSet(t) {
			timeout = parseTimeout(rt, t)
		}
	}
	_ = c.Close() // reconnecting replaces the connection
	err, code := c.dial(address.String(), plaintext, reflect, timeout)
	res := rt.NewObject()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = res.Set("error", msg)
	_ = res.Set("error_code", string(code))
	return res
}

func (c *client) dial(address string, plaintext, reflect bool, timeout time.Duration) (error, protocol.ErrorCode) {
	creds := insecure.NewCredentials()
	if !plaintext {
		cfg := &tls.Config{}
		if c.inst.run.env.TLS != nil {
			cfg = c.inst.run.env.TLS.Clone()
		}
		creds = credentials.NewTLS(cfg)
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err, protocol.CodeInvalid
	}
	ctx, cancel := context.WithTimeout(c.inst.vu.Context(), timeout)
	defer cancel()
	if err, code := waitReady(ctx, conn); err != nil {
		conn.Close()
		return err, code
	}
	if reflect {
		fs, err := c.inst.run.reflect(ctx, address, conn)
		if err != nil {
			conn.Close()
			return err, errorCode(err)
		}
		c.sets = append(c.sets, fs)
	}
	c.conn = conn
	return nil, protocol.CodeNone
}

// waitReady connects and waits for the Ready state. grpc-go retries a
// failed connection in the background; the first failure is reported
// rather than waiting out the timeout.
func waitReady(ctx context.Context, conn *grpc.ClientConn) (error, protocol.ErrorCode) {
	conn.Connect()
	for {
		switch s := conn.GetState(); s {
		case connectivity.Ready:
			return nil, protocol.CodeNone
		case connectivity.TransientFailure:
			return fmt.Errorf("could not connect to %s", conn.Target()), protocol.CodeDial
		case connectivity.Shutdown:
			return fmt.Errorf("the connection to %s was closed", conn.Target()), protocol.CodeClosed
		default:
			if !conn.WaitForStateChange(ctx, s) {
				return fmt.Errorf("could not connect to %s: %w", conn.Target(), ctx.Err()), protocol.CodeTimeout
			}
		}
	}
}

// invoke(method, request, { metadata, timeout }) makes one unary call and
// returns its result. Failures are in the result; misuse throws.
func (c *client) invoke(call goja.FunctionCall) goja.Value {
	c.requireIteration("client.invoke")
	i := c.inst
	rt := i.vu.Runtime()
	method := call.Argument(0)
	if !isSet(method) {
		panic(rt.NewTypeError("client.invoke: a method such as \"greeter.Greeter/SayHello\" is required"))
	}
	params := readParams(rt, call.Argument(2))
	res := rt.NewObject()
	fail := func(err error) goja.Value { // never sent
		code := protocol.Record(i.vu, i.run.unary, protocol.Outcome{Err: err})
		setStatus(res, err, code)
		_ = res.Set("message", goja.Null())
		return res
	}
	if c.conn == nil {
		return fail(errNotConnected)
	}
	md, fullMethod, err := findMethod(c.sets, method.String())
	if err != nil {
		return fail(err)
	}
	if md.IsStreamingClient() || md.IsStreamingServer() {
		panic(rt.NewTypeError("client.invoke: %s is a streaming method; use client.stream", fullMethod))
	}
	req, err := i.toMessage(call.Argument(1), md.Input())
	if err != nil {
		return fail(err)
	}
	reply := dynamicpb.NewMessage(md.Output())
	ctx, cancel := context.WithTimeout(i.vu.Context(), params.timeout)
	defer cancel()
	if params.md != nil {
		ctx = metadata.NewOutgoingContext(ctx, params.md)
	}
	var header, trailer metadata.MD
	start := time.Now()
	err = c.conn.Invoke(ctx, fullMethod, req, reply, grpc.Header(&header), grpc.Trailer(&trailer))
	d := time.Since(start)
	code := protocol.Record(i.vu, i.run.unary, protocol.Outcome{Duration: d, Err: err, Code: errorCode(err), Sent: true})

	setStatus(res, err, code)
	_ = res.Set("headers", mdValue(rt, header))
	_ = res.Set("trailers", mdValue(rt, trailer))
	timings := rt.NewObject()
	_ = timings.Set("duration", float64(d)/float64(time.Millisecond))
	_ = res.Set("timings", timings)
	if err != nil {
		_ = res.Set("message", goja.Null())
		return res
	}
	// The reply is converted to JavaScript only if the script reads it.
	var message goja.Value
	getter := rt.ToValue(func(goja.FunctionCall) goja.Value {
		if message == nil {
			message = i.toValue(reply)
		}
		return message
	})
	_ = res.DefineAccessorProperty("message", getter, nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	return res
}
