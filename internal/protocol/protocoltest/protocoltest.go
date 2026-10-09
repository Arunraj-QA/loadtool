// Package protocoltest runs a protocol module on its own, without the
// script, runner or engine packages (ADR-018 §11), so each protocol
// package can test itself.
package protocoltest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Harness is one VU with one module, in a goja runtime of its own. The
// module object is the global named after the module ("ws").
type Harness struct {
	t    testing.TB
	vu   *VU
	fams *metrics.Families
	run  protocol.Run
	inst protocol.Instance

	closeOnce sync.Once
	closeErr  error
}

// Option changes a Harness.
type Option func(*protocol.RunEnv)

// WithEnv changes the run environment before the run is created.
func WithEnv(f func(*protocol.RunEnv)) Option { return Option(f) }

// New creates the module's run and one VU's instance. The harness is
// closed when the test ends.
func New(t testing.TB, m protocol.Module, opts ...Option) *Harness {
	t.Helper()
	fams, err := metrics.NewFamilies(m.Metrics())
	if err != nil {
		t.Fatalf("metric families: %v", err)
	}
	vu := NewVU(1)
	env := protocol.RunEnv{Families: fams, Warn: vu.Warn, MaxVUs: 1}
	for _, o := range opts {
		o(&env)
	}
	run, err := m.NewRun(env)
	if err != nil {
		t.Fatalf("NewRun: %v", err)
	}
	vu.rec.UseFamilies(fams)
	inst, err := run.NewInstance(vu)
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	if err := vu.rt.Set(m.Name(), inst.Value()); err != nil {
		t.Fatal(err)
	}
	h := &Harness{t: t, vu: vu, fams: fams, run: run, inst: inst}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return h
}

// VU returns the harness's VU, to change its context.
func (h *Harness) VU() *VU { return h.vu }

// Run runs src in the VU's runtime with the VU's context set, as an
// iteration does: when the context ends, the script is interrupted.
func (h *Harness) Run(src string) (goja.Value, error) {
	h.inst.BeginIteration()
	ctx := h.vu.ctx
	if ctx != nil {
		stop := context.AfterFunc(ctx, func() { h.vu.rt.Interrupt("test stopped") })
		defer func() {
			stop()
			h.vu.rt.ClearInterrupt()
		}()
	}
	defer h.inst.EndIteration()
	return h.vu.rt.RunString(src)
}

// Family returns the summary of a family the module declared.
func (h *Harness) Family(name string) metrics.FamilySummary {
	h.t.Helper()
	for _, f := range h.fams.Summarize() {
		if f.Name == name {
			return f
		}
	}
	h.t.Fatalf("no metric family %q", name)
	return metrics.FamilySummary{}
}

// Close closes the instance, the resources registered with OnClose and
// the run, as the runner does after a run. It is idempotent.
func (h *Harness) Close() error {
	h.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errs := []error{h.inst.Close(ctx)}
		for _, c := range h.vu.closers {
			errs = append(errs, c.Close())
		}
		errs = append(errs, h.run.Close(ctx))
		h.closeErr = errors.Join(errs...)
	})
	return h.closeErr
}

// VU is a protocol.VU for tests.
type VU struct {
	rt       *goja.Runtime
	id       int64
	ctx      context.Context
	rec      *metrics.Recorder
	client   *http.Client
	mu       sync.Mutex
	warnings []string
	closers  []io.Closer
}

// NewVU returns a VU with a background context and a cookie jar.
func NewVU(id int64) *VU {
	jar, _ := cookiejar.New(nil)
	return &VU{rt: goja.New(), id: id, ctx: context.Background(), rec: &metrics.Recorder{},
		client: &http.Client{Jar: jar}}
}

// SetContext sets the VU's context; nil is the script's top-level code.
func (v *VU) SetContext(ctx context.Context) { v.ctx = ctx }

// Warnings returns the warnings reported so far.
func (v *VU) Warnings() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.warnings...)
}

func (v *VU) Runtime() *goja.Runtime   { return v.rt }
func (v *VU) ID() int64                { return v.id }
func (v *VU) Context() context.Context { return v.ctx }
func (v *VU) HTTPClient() *http.Client { return v.client }
func (v *VU) OnClose(c io.Closer)      { v.closers = append(v.closers, c) }

func (v *VU) Recorder() *metrics.Recorder {
	if v.ctx == nil {
		return nil
	}
	return v.rec
}

func (v *VU) Warn(msg string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.warnings = append(v.warnings, msg)
}

var _ protocol.VU = (*VU)(nil)
