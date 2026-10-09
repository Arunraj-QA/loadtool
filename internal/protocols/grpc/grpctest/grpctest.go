// Package grpctest serves the greeter service of examples/proto/greeter.proto
// for tests and the demo API. It is built from the .proto file at run time
// with dynamic messages, so it needs no protoc and no generated code, and
// it has server reflection enabled (ADR-020).
package grpctest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	greeterproto "github.com/Arunraj-QA/loadtool/examples/proto"
)

// ServiceName is the greeter service's full name.
const ServiceName = "greeter.Greeter"

var (
	compileOnce sync.Once
	files       *protoregistry.Files
	compileErr  error
)

// Files returns the compiled greeter.proto (and the files it imports).
func Files() (*protoregistry.Files, error) {
	compileOnce.Do(func() {
		c := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{"greeter.proto": greeterproto.Greeter}),
		})}
		out, err := c.Compile(context.Background(), "greeter.proto")
		if err != nil {
			compileErr = err
			return
		}
		files = new(protoregistry.Files)
		for _, f := range out {
			if err := files.RegisterFile(f); err != nil {
				compileErr = err
				return
			}
		}
	})
	return files, compileErr
}

// Register adds the greeter service, and server reflection for it, to srv.
// SayHello waits delay before answering, and LotsOfReplies before each
// reply.
func Register(srv *grpc.Server, delay time.Duration) error {
	fs, err := Files()
	if err != nil {
		return err
	}
	d, err := fs.FindDescriptorByName(ServiceName)
	if err != nil {
		return err
	}
	g := &greeter{svc: d.(protoreflect.ServiceDescriptor), delay: delay}
	srv.RegisterService(g.desc(), g)
	reflectionpb.RegisterServerReflectionServer(srv, reflection.NewServerV1(reflection.ServerOptions{
		Services: srv, DescriptorResolver: fs,
	}))
	return nil
}

type greeter struct {
	svc   protoreflect.ServiceDescriptor
	delay time.Duration
}

func (g *greeter) desc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: ServiceName,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "SayHello", Handler: g.unary("SayHello", g.sayHello)},
			{MethodName: "Fail", Handler: g.unary("Fail", g.fail)},
		},
		Streams: []grpc.StreamDesc{
			{StreamName: "LotsOfReplies", Handler: g.lotsOfReplies, ServerStreams: true},
			{StreamName: "LotsOfGreetings", Handler: g.lotsOfGreetings, ClientStreams: true},
			{StreamName: "Chat", Handler: g.chat, ServerStreams: true, ClientStreams: true},
		},
		Metadata: "greeter.proto",
	}
}

func (g *greeter) method(name string) protoreflect.MethodDescriptor {
	return g.svc.Methods().ByName(protoreflect.Name(name))
}

func (g *greeter) unary(name string, h func(context.Context, *dynamicpb.Message) (*dynamicpb.Message, error)) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		req := dynamicpb.NewMessage(g.method(name).Input())
		if err := dec(req); err != nil {
			return nil, err
		}
		return h(ctx, req)
	}
}

func str(m *dynamicpb.Message, field string) string {
	return m.Get(m.Descriptor().Fields().ByName(protoreflect.Name(field))).String()
}

func num(m *dynamicpb.Message, field string) int64 {
	return m.Get(m.Descriptor().Fields().ByName(protoreflect.Name(field))).Int()
}

func (g *greeter) reply(text string, index int) *dynamicpb.Message {
	m := dynamicpb.NewMessage(g.method("SayHello").Output())
	fields := m.Descriptor().Fields()
	m.Set(fields.ByName("message"), protoreflect.ValueOfString(text))
	m.Set(fields.ByName("index"), protoreflect.ValueOfInt32(int32(index)))
	return m
}

// sayHello answers "Hello, <name>" after the delay. It echoes the
// request's x-request-id metadata as a header and sets a trailer.
func (g *greeter) sayHello(ctx context.Context, req *dynamicpb.Message) (*dynamicpb.Message, error) {
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get("x-request-id")) > 0 {
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", md.Get("x-request-id")[0]))
	}
	_ = grpc.SetTrailer(ctx, metadata.Pairs("x-served-by", "grpctest"))
	return g.reply("Hello, "+str(req, "name"), 0), nil
}

// fail returns the status the request names.
func (g *greeter) fail(_ context.Context, req *dynamicpb.Message) (*dynamicpb.Message, error) {
	return nil, status.Error(codes.Code(num(req, "code")), str(req, "message"))
}

// failure returns the status a request's fail_code names, or nil.
func failure(req *dynamicpb.Message) error {
	if c := num(req, "fail_code"); c != 0 {
		return status.Errorf(codes.Code(c), "failed as asked (fail_code %d)", c)
	}
	return nil
}

// lotsOfReplies sends `count` replies (default 3), then ends with the
// request's fail_code status, if any.
func (g *greeter) lotsOfReplies(_ any, stream grpc.ServerStream) error {
	req := dynamicpb.NewMessage(g.method("LotsOfReplies").Input())
	if err := stream.RecvMsg(req); err != nil {
		return err
	}
	n := int(num(req, "count"))
	if n == 0 {
		n = 3
	}
	for i := range n {
		if err := g.wait(stream.Context()); err != nil {
			return err
		}
		if err := stream.SendMsg(g.reply(fmt.Sprintf("Hello %d, %s", i, str(req, "name")), i)); err != nil {
			return err
		}
	}
	return failure(req)
}

// lotsOfGreetings answers once, naming every request it received; a
// request with fail_code makes it end with that status instead.
func (g *greeter) lotsOfGreetings(_ any, stream grpc.ServerStream) error {
	var names []string
	var fail error
	for {
		req := dynamicpb.NewMessage(g.method("LotsOfGreetings").Input())
		err := stream.RecvMsg(req)
		if errors.Is(err, io.EOF) {
			if fail != nil {
				return fail
			}
			return stream.SendMsg(g.reply("Hello, "+strings.Join(names, ", "), len(names)))
		}
		if err != nil {
			return err
		}
		names = append(names, str(req, "name"))
		if fail == nil {
			fail = failure(req)
		}
	}
}

// chat answers each request as it arrives; a request with fail_code
// ends the stream with that status.
func (g *greeter) chat(_ any, stream grpc.ServerStream) error {
	for i := 0; ; i++ {
		req := dynamicpb.NewMessage(g.method("Chat").Input())
		err := stream.RecvMsg(req)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := failure(req); err != nil {
			return err
		}
		if err := stream.SendMsg(g.reply("Hello, "+str(req, "name"), i)); err != nil {
			return err
		}
	}
}

// wait waits the configured delay, or until the call is cancelled.
func (g *greeter) wait(ctx context.Context) error {
	if g.delay <= 0 {
		return nil
	}
	t := time.NewTimer(g.delay)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
}
