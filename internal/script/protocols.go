package script

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// moduleSet is a program's protocol modules (ADR-014, ADR-018). It is
// shared by every copy of the Program (the With* methods copy the pointer)
// and so by every VU. imported is written while bundling; runs and
// families are written once by StartModules, before any VU iterates, and
// only read afterwards.
type moduleSet struct {
	all []protocol.Module
	// props are the builtin object's properties for the modules; built
	// once, shared by every VU.
	props []lazyProp
	// vuProps are a VU's builtin object properties: the core ones and
	// those of the modules the script imports, so a module it does not
	// import costs its VUs nothing. Set by seal, after bundling.
	vuProps []lazyProp

	mu       sync.Mutex // guards imported while esbuild resolves imports
	imported []bool     // by index in all

	runs     []protocol.Run // by index in all; nil if not imported
	families *metrics.Families
}

// reservedBuiltins are names of the builtin object's own properties.
var reservedBuiltins = []string{"http", "core", "exec"}

func newModuleSet(mods []protocol.Module) (*moduleSet, error) {
	ms := &moduleSet{all: mods, imported: make([]bool, len(mods)), runs: make([]protocol.Run, len(mods))}
	seen := map[string]bool{}
	for i, m := range mods {
		for _, name := range append([]string{m.Name()}, aliases(m)...) {
			if slices.Contains(reservedBuiltins, name) {
				return nil, fmt.Errorf("protocol module %q: the name is reserved", name)
			}
			if seen[name] {
				return nil, fmt.Errorf("protocol module name %q is registered twice", name)
			}
			seen[name] = true
		}
		ms.props = append(ms.props, lazyProp{m.Name(), func(vu *VU) goja.Value { return vu.newModuleObject(i) }})
	}
	return ms, nil
}

// index returns the index of the module imported as path ("loadtool/ws"),
// by its name or one of its aliases.
func (ms *moduleSet) index(path string) (int, bool) {
	if ms == nil {
		return 0, false
	}
	name, ok := strings.CutPrefix(path, "loadtool/")
	if !ok {
		return 0, false
	}
	for i, m := range ms.all {
		if m.Name() == name || slices.Contains(aliases(m), name) {
			return i, true
		}
	}
	return 0, false
}

func aliases(m protocol.Module) []string {
	if a, ok := m.(protocol.Aliased); ok {
		return a.Aliases()
	}
	return nil
}

// seal sets vuProps from the imports bundling found. It runs once, before
// any VU is created.
func (ms *moduleSet) seal() {
	ms.vuProps = builtinProps
	for i, imp := range ms.imported {
		if imp {
			ms.vuProps = append(slices.Clip(ms.vuProps), ms.props[i])
		}
	}
}

// importsAny reports whether the script imports any protocol module.
func (ms *moduleSet) importsAny() bool { return ms != nil && len(ms.vuProps) > len(builtinProps) }

// markImported records that the script imports module i.
func (ms *moduleSet) markImported(i int) {
	ms.mu.Lock()
	ms.imported[i] = true
	ms.mu.Unlock()
}

// source is the JavaScript of a protocol module: it re-exports the
// functions of the Go-backed module object, as the HTTP module does.
func (ms *moduleSet) source(i int) string {
	m := ms.all[i]
	var b strings.Builder
	fmt.Fprintf(&b, "const m = globalThis.%s[%q];\nexport default m;\n", builtinGlobal, m.Name())
	for _, e := range m.Exports() {
		fmt.Fprintf(&b, "export const %s = /* @__PURE__ */ (() => m.%s)();\n", e, e)
	}
	return b.String()
}

// names lists the importable built-in module paths, for error messages.
func (ms *moduleSet) names() []string {
	names := []string{`"loadtool"`, `"loadtool/http"`}
	if ms != nil {
		for _, m := range ms.all {
			names = append(names, fmt.Sprintf("%q", "loadtool/"+m.Name()))
			for _, a := range aliases(m) {
				names = append(names, fmt.Sprintf("%q", "loadtool/"+a))
			}
		}
	}
	return names
}

// StartModules creates the run of every protocol module the script
// imports, with their metric families, before setup. It returns the
// families' definitions, which thresholds may name.
func (p *Program) StartModules(env protocol.RunEnv) ([]metrics.Def, error) {
	ms := p.mods
	if ms == nil {
		return nil, nil
	}
	var defs []metrics.Def
	for i, m := range ms.all {
		if ms.imported[i] {
			defs = append(defs, m.Metrics()...)
		}
	}
	if len(defs) == 0 {
		return nil, nil
	}
	fams, err := metrics.NewFamilies(defs)
	if err != nil {
		return nil, err
	}
	env.Families = fams
	env.Warn = p.warn.once // once per run, however many VUs report it
	env.Dir = p.dir
	for i, m := range ms.all {
		if !ms.imported[i] {
			continue
		}
		run, err := m.NewRun(env)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("loadtool/%s: %w", m.Name(), err), p.CloseModules(context.Background()))
		}
		ms.runs[i] = run
	}
	ms.families = fams
	return defs, nil
}

// ModuleFamilies returns the merged protocol metric families; nil
// without any.
func (p *Program) ModuleFamilies() []metrics.FamilySummary {
	if p.mods == nil {
		return nil
	}
	return p.mods.families.Summarize()
}

// CloseModules closes every module run, after every VU closed.
func (p *Program) CloseModules(ctx context.Context) error {
	if p.mods == nil {
		return nil
	}
	var errs []error
	for i, r := range p.mods.runs {
		if r != nil {
			errs = append(errs, r.Close(ctx))
			p.mods.runs[i] = nil
		}
	}
	return errors.Join(errs...)
}

// newModuleObject builds the module object of module i in this VU: one
// function per export, each calling into the VU's instance (see
// callModule). A capitalized export is a class (new grpc.Client()), built
// with goja's constructor form. The object is built on first access, like
// every builtin module.
func (vu *VU) newModuleObject(i int) goja.Value {
	m := vu.mods.all[i]
	props := make([]lazyProp, 0, len(m.Exports()))
	for _, e := range m.Exports() {
		if isClassName(e) {
			props = append(props, lazyProp{e, func(vu *VU) goja.Value {
				return vu.rt.ToValue(func(call goja.ConstructorCall) *goja.Object { return vu.constructModule(i, e, call) })
			}})
			continue
		}
		props = append(props, lazyProp{e, func(vu *VU) goja.Value {
			return vu.rt.ToValue(func(call goja.FunctionCall) goja.Value { return vu.callModule(i, e, call) })
		}})
	}
	return vu.newLazyObject(props)
}

func isClassName(name string) bool { return name != "" && name[0] >= 'A' && name[0] <= 'Z' }

// instanceFor returns this VU's instance of module i, creating it on first
// use.
func (vu *VU) instanceFor(i int) protocol.Instance {
	inst := vu.insts[i]
	if inst == nil {
		run := vu.mods.runs[i]
		if run == nil {
			panic(vu.rt.NewGoError(fmt.Errorf("loadtool/%s cannot be used before the test starts", vu.mods.all[i].Name())))
		}
		var err error
		if inst, err = run.NewInstance(vu); err != nil {
			panic(vu.rt.NewGoError(fmt.Errorf("loadtool/%s: %w", vu.mods.all[i].Name(), err)))
		}
		vu.insts[i] = inst
	}
	return inst
}

// constructModule builds class name of module i, with or without new.
func (vu *VU) constructModule(i int, name string, call goja.ConstructorCall) *goja.Object {
	inst := vu.instanceFor(i)
	ctor, ok := goja.AssertConstructor(inst.Value().ToObject(vu.rt).Get(name))
	if !ok {
		panic(vu.rt.NewTypeError("loadtool/%s has no class %s", vu.mods.all[i].Name(), name))
	}
	obj, err := ctor(nil, call.Arguments...)
	if err != nil {
		panic(err)
	}
	return obj
}

// callModule calls export name of module i on this VU's instance,
// creating the instance on first use.
func (vu *VU) callModule(i int, name string, call goja.FunctionCall) goja.Value {
	inst := vu.instanceFor(i)
	fn, ok := goja.AssertFunction(inst.Value().ToObject(vu.rt).Get(name))
	if !ok {
		panic(vu.rt.NewTypeError("loadtool/%s has no function %s", vu.mods.all[i].Name(), name))
	}
	v, err := fn(call.This, call.Arguments...)
	if err != nil {
		panic(err)
	}
	return v
}

// beginIteration attaches the run's families to rec and tells every
// instance this VU built that an iteration starts.
func (vu *VU) beginIteration(rec *metrics.Recorder) {
	if vu.mods == nil {
		return
	}
	if fams := vu.mods.families; fams != nil {
		rec.UseFamilies(fams)
	}
	for _, inst := range vu.insts {
		if inst != nil {
			inst.BeginIteration()
		}
	}
}

// endIteration tells every instance this VU built that the iteration (or
// setup, or teardown) returned. It runs while the context is still set.
func (vu *VU) endIteration() {
	for _, inst := range vu.insts {
		if inst != nil {
			inst.EndIteration()
		}
	}
}

// Close closes the VU's module instances, then the resources its scripts
// registered with OnClose. It is idempotent; the VU must not iterate
// again.
func (vu *VU) Close(ctx context.Context) error {
	var errs []error
	for i, inst := range vu.insts {
		if inst != nil {
			errs = append(errs, inst.Close(ctx))
			vu.insts[i] = nil
		}
	}
	for _, c := range vu.closers {
		errs = append(errs, c.Close())
	}
	vu.closers = nil
	return errors.Join(errs...)
}

// protocol.VU, for the modules.

var _ protocol.VU = (*VU)(nil)

func (vu *VU) Runtime() *goja.Runtime   { return vu.rt }
func (vu *VU) ID() int64                { return vu.id }
func (vu *VU) Context() context.Context { return vu.ctx }
func (vu *VU) HTTPClient() *http.Client { return vu.client }
func (vu *VU) OnClose(c io.Closer)      { vu.closers = append(vu.closers, c) }
func (vu *VU) Warn(msg string)          { vu.warn.once(msg) }

func (vu *VU) Recorder() *metrics.Recorder {
	if vu.ctx == nil {
		return nil
	}
	return vu.rec
}
