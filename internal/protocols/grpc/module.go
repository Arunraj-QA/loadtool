// Package grpc is the gRPC protocol module, imported by scripts as
// "loadtool/grpc" (ADR-020). It uses grpc-go for the wire protocol and
// dynamic protobuf messages, so methods are described at run time by
// .proto files (client.load) or server reflection (connect with reflect).
//
//	const client = new grpc.Client();
//	client.load(["proto"], "greeter.proto");
//	export default function () {
//	  if (__ITER === 0) client.connect("127.0.0.1:8091", { plaintext: true });
//	  const res = client.invoke("greeter.Greeter/SayHello", { name: "Ada" });
//	}
//
// Each client belongs to the VU that created it; its connection is kept
// across iterations and closed at the end of the run. Streams belong to
// the iteration that opened them.
package grpc

import (
	"context"
	"sync"
	"time"

	"github.com/dop251/goja"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Metric family names (ADR-015, ADR-020).
const (
	MetricReqDuration        = "grpc_req_duration"
	MetricReqs               = "grpc_reqs"
	MetricReqFailed          = "grpc_req_failed"
	MetricStreams            = "grpc_streams"
	MetricStreamDuration     = "grpc_stream_duration"
	MetricStreamFailed       = "grpc_stream_failed"
	MetricStreamMsgsSent     = "grpc_stream_msgs_sent"
	MetricStreamMsgsReceived = "grpc_stream_msgs_received"
)

// defaultTimeout bounds connect, each unary call and each stream when the
// script gives no timeout, as httpclient.DefaultTimeout bounds HTTP.
const defaultTimeout = 30 * time.Second

// Module is the gRPC protocol module.
type Module struct{}

var _ protocol.Module = Module{}

func (Module) Name() string      { return "grpc" }
func (Module) Exports() []string { return []string{"Client"} }

func (Module) Metrics() []metrics.Def {
	return []metrics.Def{
		{Name: MetricReqDuration, Kind: metrics.Trend},
		{Name: MetricReqs, Kind: metrics.Counter},
		{Name: MetricReqFailed, Kind: metrics.Rate},
		{Name: MetricStreams, Kind: metrics.Counter},
		{Name: MetricStreamDuration, Kind: metrics.Trend},
		{Name: MetricStreamFailed, Kind: metrics.Rate},
		{Name: MetricStreamMsgsSent, Kind: metrics.Counter},
		{Name: MetricStreamMsgsReceived, Kind: metrics.Counter},
	}
}

// run is the module's state for one test run. Its caches hold read-only
// descriptors, shared by every VU's clients.
type run struct {
	unary, stream           protocol.Families
	streams, sent, received metrics.FamilyID
	env                     protocol.RunEnv
	warn                    func(string)

	mu        sync.Mutex
	loaded    map[string]*cached // by import paths and files
	reflected map[string]*cached // by address
}

// cached is one descriptor set, loaded once even when many VUs ask at the
// same time.
type cached struct {
	once  sync.Once
	files *protoregistry.Files
	err   error
}

func (Module) NewRun(env protocol.RunEnv) (protocol.Run, error) {
	ids, err := protocol.FamilyIDs(env, MetricReqDuration, MetricReqs, MetricReqFailed,
		MetricStreams, MetricStreamDuration, MetricStreamFailed, MetricStreamMsgsSent, MetricStreamMsgsReceived)
	if err != nil {
		return nil, err
	}
	warn := env.Warn
	if warn == nil {
		warn = func(string) {}
	}
	return &run{
		unary:     protocol.Families{Duration: ids[0], Count: ids[1], Failed: ids[2]},
		streams:   ids[3],
		stream:    protocol.Families{Duration: ids[4], Count: protocol.NoFamily, Failed: ids[5]},
		sent:      ids[6],
		received:  ids[7],
		env:       env,
		warn:      warn,
		loaded:    map[string]*cached{},
		reflected: map[string]*cached{},
	}, nil
}

// cache returns the entry for key in m, creating it.
func (r *run) cache(m map[string]*cached, key string) *cached {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := m[key]
	if !ok {
		c = &cached{}
		m[key] = c
	}
	return c
}

func (r *run) NewInstance(vu protocol.VU) (protocol.Instance, error) {
	return &instance{vu: vu, run: r}, nil
}

func (r *run) Close(context.Context) error { return nil }

// instance is the module in one VU. Clients are registered with
// VU.OnClose; open holds the streams the current iteration opened.
type instance struct {
	vu   protocol.VU
	run  *run
	obj  *goja.Object
	open []*stream
	// json caches the runtime's JSON.stringify and JSON.parse.
	stringify, parse goja.Callable
}

func (i *instance) Value() goja.Value {
	if i.obj == nil {
		rt := i.vu.Runtime()
		i.obj = rt.NewObject()
		_ = i.obj.Set("Client", func(call goja.ConstructorCall) *goja.Object {
			return i.newClient(call.This)
		})
	}
	return i.obj
}

func (i *instance) BeginIteration() {}

// EndIteration cancels the streams the iteration left open.
func (i *instance) EndIteration() {
	for _, s := range i.open {
		if !s.done {
			i.vu.Warn("grpc: a stream was still open when the iteration ended; it was closed for you (call stream.close() or read it to the end)")
			break
		}
	}
	i.closeStreams()
}

func (i *instance) Close(context.Context) error {
	i.closeStreams()
	return nil
}

func (i *instance) closeStreams() {
	for _, s := range i.open {
		s.close()
	}
	i.open = nil
}
