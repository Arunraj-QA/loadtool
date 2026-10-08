// Package protocol defines how protocol modules (WebSocket, and later
// gRPC, GraphQL and Kafka) plug into LoadTool (ADR-014, ADR-018).
//
// A Module is compiled in and listed by the runner. For a test run that
// imports it, the module gets a Run (shared state, such as transports),
// and each VU that uses it gets an Instance (that VU's state). Modules
// never see scenarios, executors or the command line: the VU interface is
// all they get from the rest of LoadTool.
//
// Concurrency (ADR-018 §10): JavaScript runs only on the goroutine
// running the VU, and so does every call into a module. A module may
// start goroutines only inside a call, joined before it returns, or owned
// by a Run or Instance and stopped by its Close; they never touch goja or
// a metrics.Recorder.
package protocol

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// Module is a protocol module, imported by scripts as "loadtool/<Name>".
type Module interface {
	// Name is the module's import name, such as "ws".
	Name() string
	// Exports are the functions the module object provides; each is also
	// a named export of the JavaScript module.
	Exports() []string
	// Metrics are the metric families the module records (ADR-015).
	Metrics() []metrics.Def
	// NewRun creates the module's state for one test run. It is called
	// only when the script imports the module.
	NewRun(RunEnv) (Run, error)
}

// RunEnv is what a module gets from the run. It carries no scenario or
// command-line state.
type RunEnv struct {
	// Families are the run's metric families, including this module's;
	// modules look up their family IDs here.
	Families *metrics.Families
	// TLS replaces the default TLS settings when set (tests use it).
	TLS *tls.Config
	// Warn reports a problem once per run; nil discards it.
	Warn func(msg string)
	// MaxVUs is the most VUs the run has at once, for sizing pools.
	MaxVUs int
}

// Run is a module's state for one test run, shared by its VUs. It must
// be safe for use by several VUs' goroutines at once.
type Run interface {
	// NewInstance creates the module's state in one VU.
	NewInstance(VU) (Instance, error)
	// Close releases the run's resources, after every instance closed.
	Close(ctx context.Context) error
}

// Instance is a module's state in one VU. It is used only on the
// goroutine running that VU.
type Instance interface {
	// Value is the module object: an object whose properties are the
	// functions listed in Module.Exports.
	Value() goja.Value
	// BeginIteration is called at the start of each of the VU's
	// iterations.
	BeginIteration()
	// Close releases the instance's resources. It must be idempotent and
	// return within ctx's deadline.
	Close(ctx context.Context) error
}

// VU is what a module may use from the VU it belongs to.
type VU interface {
	Runtime() *goja.Runtime
	// ID is __VU: 1 and up for VUs, 0 for the setup/teardown runtime.
	ID() int64
	// Context is the current iteration's (or setup's, or teardown's)
	// context; nil in the script's top-level code, where network calls
	// are not allowed.
	Context() context.Context
	// Recorder receives metrics; nil when Context is. In setup and
	// teardown it is a throwaway recorder, so nothing there is counted.
	Recorder() *metrics.Recorder
	// HTTPClient is the VU's HTTP session: the run's transport with this
	// VU's cookie jar.
	HTTPClient() *http.Client
	// Warn reports a problem once per run.
	Warn(msg string)
	// OnClose registers a resource the script created; it is closed with
	// the VU's instances.
	OnClose(io.Closer)
}
