package grpc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// load parses .proto files, relative to the import paths (themselves
// relative to the script's directory). The result is cached for the run.
func (r *run) load(importPaths, files []string) (*protoregistry.Files, error) {
	paths := make([]string, len(importPaths))
	for i, p := range importPaths {
		if !filepath.IsAbs(p) && r.env.Dir != "" {
			p = filepath.Join(r.env.Dir, p)
		}
		paths[i] = p
	}
	if len(paths) == 0 && r.env.Dir != "" {
		paths = []string{r.env.Dir}
	}
	key := strings.Join(paths, "\x00") + "\x01" + strings.Join(files, "\x00")
	c := r.cache(r.loaded, key)
	c.once.Do(func() {
		compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: paths})}
		out, err := compiler.Compile(context.Background(), files...)
		if err != nil {
			c.err = err
			return
		}
		fs := new(protoregistry.Files)
		for _, f := range out {
			if err := registerWithImports(fs, f); err != nil {
				c.err = err
				return
			}
		}
		c.files = fs
	})
	return c.files, c.err
}

// registerWithImports registers f and, first, the files it imports.
func registerWithImports(fs *protoregistry.Files, f protoreflect.FileDescriptor) error {
	if _, err := fs.FindFileByPath(f.Path()); err == nil {
		return nil
	}
	imports := f.Imports()
	for i := range imports.Len() {
		if err := registerWithImports(fs, imports.Get(i).FileDescriptor); err != nil {
			return err
		}
	}
	return fs.RegisterFile(f)
}

// reflect fetches the descriptors of every service the server at address
// offers, by server reflection (v1). The result is cached for the run.
func (r *run) reflect(ctx context.Context, address string, conn *grpc.ClientConn) (*protoregistry.Files, error) {
	c := r.cache(r.reflected, address)
	c.once.Do(func() { c.files, c.err = fetchDescriptors(ctx, conn) })
	if c.err != nil {
		// Do not cache a failure: the next VU (or iteration) may succeed.
		r.mu.Lock()
		if r.reflected[address] == c {
			delete(r.reflected, address)
		}
		r.mu.Unlock()
	}
	return c.files, c.err
}

func fetchDescriptors(ctx context.Context, conn *grpc.ClientConn) (*protoregistry.Files, error) {
	stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("server reflection: %w", err)
	}
	defer stream.CloseSend()
	ask := func(req *reflectionpb.ServerReflectionRequest) (*reflectionpb.ServerReflectionResponse, error) {
		if err := stream.Send(req); err != nil {
			return nil, err
		}
		res, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		if e := res.GetErrorResponse(); e != nil {
			return nil, fmt.Errorf("server reflection: %s (code %d)", e.GetErrorMessage(), e.GetErrorCode())
		}
		return res, nil
	}
	res, err := ask(&reflectionpb.ServerReflectionRequest{MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{}})
	if err != nil {
		return nil, fmt.Errorf("server reflection: %w", err)
	}
	protos := map[string]*descriptorpb.FileDescriptorProto{}
	for _, svc := range res.GetListServicesResponse().GetService() {
		if strings.HasPrefix(svc.GetName(), "grpc.reflection.") {
			continue
		}
		res, err := ask(&reflectionpb.ServerReflectionRequest{MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: svc.GetName()}})
		if err != nil {
			return nil, fmt.Errorf("server reflection for %s: %w", svc.GetName(), err)
		}
		for _, raw := range res.GetFileDescriptorResponse().GetFileDescriptorProto() {
			fd := new(descriptorpb.FileDescriptorProto)
			if err := proto.Unmarshal(raw, fd); err != nil {
				return nil, fmt.Errorf("server reflection: %w", err)
			}
			protos[fd.GetName()] = fd
		}
	}
	return buildFiles(protos)
}

// buildFiles turns file descriptor protos into a registry. Imports the
// server did not send (well-known types) come from the global registry.
func buildFiles(protos map[string]*descriptorpb.FileDescriptorProto) (*protoregistry.Files, error) {
	var add func(name string) error
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	add = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		fd, ok := protos[name]
		if !ok {
			global, err := protoregistry.GlobalFiles.FindFileByPath(name)
			if err != nil {
				return fmt.Errorf("server reflection: the server did not send %s", name)
			}
			fd = protodesc.ToFileDescriptorProto(global)
		}
		for _, dep := range fd.GetDependency() {
			if err := add(dep); err != nil {
				return err
			}
		}
		set.File = append(set.File, fd)
		return nil
	}
	for name := range protos {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	return protodesc.NewFiles(set)
}

// errUnknownMethod is returned for a method no loaded descriptor has.
var errUnknownMethod = errors.New("unknown method")

// findMethod finds "package.Service/Method" (a leading "/" is allowed).
func findMethod(sets []*protoregistry.Files, method string) (protoreflect.MethodDescriptor, string, error) {
	m := strings.TrimPrefix(method, "/")
	svc, name, ok := strings.Cut(m, "/")
	if !ok || svc == "" || name == "" {
		return nil, "", fmt.Errorf("%w %q: write it as \"package.Service/Method\"", errUnknownMethod, method)
	}
	for _, fs := range sets {
		d, err := fs.FindDescriptorByName(protoreflect.FullName(svc))
		if err != nil {
			continue
		}
		sd, ok := d.(protoreflect.ServiceDescriptor)
		if !ok {
			continue
		}
		if md := sd.Methods().ByName(protoreflect.Name(name)); md != nil {
			return md, "/" + svc + "/" + name, nil
		}
	}
	return nil, "", fmt.Errorf("%w %q: load its .proto file, or connect with reflect: true", errUnknownMethod, method)
}
