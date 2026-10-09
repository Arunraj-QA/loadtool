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
// Unary SayHello waits delay before answering.
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
	if g.delay > 0 {
		t := time.NewTimer(g.delay)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
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

// lotsOfReplies sends `count` replies (default 3).
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
		if err := stream.SendMsg(g.reply(fmt.Sprintf("Hello %d, %s", i, str(req, "name")), i)); err != nil {
			return err
		}
	}
	return nil
}

// lotsOfGreetings answers once, naming every request it received.
func (g *greeter) lotsOfGreetings(_ any, stream grpc.ServerStream) error {
	var names []string
	for {
		req := dynamicpb.NewMessage(g.method("LotsOfGreetings").Input())
		err := stream.RecvMsg(req)
		if errors.Is(err, io.EOF) {
			return stream.SendMsg(g.reply("Hello, "+strings.Join(names, ", "), len(names)))
		}
		if err != nil {
			return err
		}
		names = append(names, str(req, "name"))
	}
}

// chat answers each request as it arrives.
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
		if err := stream.SendMsg(g.reply("Hello, "+str(req, "name"), i)); err != nil {
			return err
		}
	}
}
