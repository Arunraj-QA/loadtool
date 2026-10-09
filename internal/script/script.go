// Package script compiles TypeScript/JavaScript test files and runs them in
// goja, with one isolated JavaScript runtime per VU.
//
// Concurrency model:
//   - A Program is compiled once and is immutable; goja documents compiled
//     programs as safe for concurrent use, so all VUs share it.
//   - Each VU owns its own goja.Runtime. A Runtime is not goroutine-safe and
//     must only be used by the goroutine that runs the VU. No JavaScript
//     values are shared between VUs.
//   - The only cross-goroutine call is Runtime.Interrupt, which goja
//     documents as safe, used to stop a script when the test ends.
package script

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Program is a compiled test script, safe to share between VUs.
type Program struct {
	prog *goja.Program
	// filename, dir and src are kept so WithExecs can rebuild the entry.
	filename, dir string
	src           []byte
	// execs are the exported functions, besides default, that scenarios
	// run (WithExecs); sorted.
	execs []string
	// env backs every VU's __ENV. It is never written after WithEnv, so
	// VUs on different goroutines read it safely.
	env map[string]string
	// console receives console output from all VUs; nil discards it.
	console *lockedWriter
	// discardBodies drops response bodies (options.discardResponseBodies).
	discardBodies bool
	// keepCookies keeps each VU's cookies across iterations
	// (options.noCookiesReset, ADR-009).
	keepCookies bool
	// setupData is the JSON setup() returned, nil without setup or data.
	// Read-only once set; each VU parses its own copy.
	setupData []byte
	// warn reports non-fatal problems found while the script runs.
	warn *warner
	// mods are the protocol modules (ADR-018); nil without any.
	mods *moduleSet
}

// WithSetupData returns a copy of p whose VUs pass data, the JSON that
// setup() returned, to every iteration. data must not be modified
// afterwards; nil means no data (the default function gets undefined).
func (p *Program) WithSetupData(data []byte) *Program {
	c := *p
	c.setupData = data
	return &c
}

// WithKeepCookies returns a copy of p whose VUs keep their cookies across
// iterations; by default each iteration starts with an empty jar.
func (p *Program) WithKeepCookies(keep bool) *Program {
	c := *p
	c.keepCookies = keep
	return &c
}

// WithDiscardResponseBodies returns a copy of p whose VUs drop response
// bodies instead of handing them to the script.
func (p *Program) WithDiscardResponseBodies(discard bool) *Program {
	c := *p
	c.discardBodies = discard
	return &c
}

// WithWarn returns a copy of p whose VUs report non-fatal problems, such
// as unsupported request parameters, to warn. Each message is reported
// once per run, however many VUs hit it; nil discards them.
func (p *Program) WithWarn(warn func(msg string)) *Program {
	c := *p
	c.warn = newWarner(warn)
	return &c
}

// WithEnv returns a copy of p whose VUs see env as __ENV. env must not be
// modified afterwards.
func (p *Program) WithEnv(env map[string]string) *Program {
	c := *p
	c.env = env
	return &c
}

// Load reads and compiles the script at path. Relative imports
// (./helpers.ts) are resolved from the script's directory. modules are the
// protocol modules the script may import as "loadtool/<name>".
func Load(path string, modules ...protocol.Module) (*Program, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return compileIn(filepath.Base(path), filepath.Dir(abs), src, modules)
}

const (
	// scriptImport is how the generated entry imports the user's script.
	scriptImport = "loadtool:script"
	// scriptNamespace is the esbuild namespace the script is loaded in;
	// esbuild prefixes file names with it, so it is stripped for users.
	scriptNamespace = "loadtool"
	// defaultExportGlobal is where the entry stores the default export.
	defaultExportGlobal = "__loadtool_default"
	// optionsGlobal is where the entry stores `export const options`.
	optionsGlobal = "__loadtool_options"
	// setupGlobal and teardownGlobal hold the setup and teardown exports.
	setupGlobal    = "__loadtool_setup"
	teardownGlobal = "__loadtool_teardown"
	// outfile names the in-memory build output; nothing is written to disk.
	outfile = "script.js"
)

// entrySource stores the script's default export, options, setup and
// teardown in globals. Bundling this entry lets esbuild bind the exports directly
// (property reads on a namespace import become plain references), so the
// output needs no CommonJS interop helpers. Every VU runs the output, and
// those helpers made up about 75% of per-VU memory (see
// benchmarks/results/2026-09-24-vu-memory.md). A missing export becomes
// undefined, not a build error.
var entrySource = fmt.Sprintf("import * as mod from %q;\n"+
	"globalThis.%s = mod.default;\nglobalThis.%s = mod.options;\n"+
	"if (__VU === 0) { globalThis.%s = mod.setup; globalThis.%s = mod.teardown; }\n",
	scriptImport, defaultExportGlobal, optionsGlobal, setupGlobal, teardownGlobal)

// ErrNoDefaultExport is returned when a VU should run the default export
// and the script has none.
var ErrNoDefaultExport = errors.New("script must export a default function: `export default function () { ... }`")

// Compile transpiles src to JavaScript (stripping TypeScript types and
// resolving the default export) and compiles it for goja. Files ending in
// .ts are treated as TypeScript, everything else as JavaScript. The script
// may import built-in modules; relative imports need Load, which knows the
// script's directory.
func Compile(filename string, src []byte, modules ...protocol.Module) (*Program, error) {
	return compileIn(filename, "", src, modules)
}

// compileIn is Compile with the directory relative imports resolve from;
// dir must be absolute, or empty to reject relative imports.
func compileIn(filename, dir string, src []byte, modules []protocol.Module) (*Program, error) {
	var mods *moduleSet
	if len(modules) > 0 {
		var err error
		if mods, err = newModuleSet(modules); err != nil {
			return nil, err
		}
	}
	code, err := transpile(filename, dir, src, mods)
	if err != nil {
		return nil, err
	}
	prog, err := goja.Compile(filename, code, true)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", filename, err)
	}
	return &Program{prog: prog, filename: filename, dir: dir, src: src, mods: mods}, nil
}

// transpile turns the script, and the files it imports, into one plain
// JavaScript program that stores its default export in defaultExportGlobal.
func transpile(filename, dir string, src []byte, mods *moduleSet) (string, error) {
	return transpileEntry(filename, dir, src, entrySource, mods)
}

// transpileEntry is transpile with a given generated entry.
func transpileEntry(filename, dir string, src []byte, entry string, mods *moduleSet) (string, error) {
	loader := api.LoaderJS
	if strings.EqualFold(filepath.Ext(filename), ".ts") {
		loader = api.LoaderTS
	}
	out := api.Build(api.BuildOptions{
		Stdin:  &api.StdinOptions{Contents: entry},
		Bundle: true,
		// IIFE keeps top-level declarations module-scoped, as in an ES
		// module. As globals they would cost every VU about 2 KB more.
		Format:   api.FormatIIFE,
		Platform: api.PlatformNeutral,
		Target:   api.ES2017,
		Plugins:  []api.Plugin{scriptPlugin(filename, dir, string(src), loader, mods)},
		// Paths in errors and the source map are relative to the script's
		// directory, so they read "helpers.ts", not a full path.
		AbsWorkingDir: dir,
		// The source map lets goja report errors at .ts line numbers. It is
		// produced separately so its file names can be cleaned up.
		Sourcemap: api.SourceMapExternal,
		Outfile:   outfile,
		LogLevel:  api.LogLevelSilent,
	})
	if len(out.Errors) > 0 {
		return "", buildError(filename, out.Errors)
	}
	code, err := withInlineSourceMap(out.OutputFiles)
	if err != nil {
		return "", fmt.Errorf("compile %s: %w", filename, err)
	}
	return code, nil
}

// withInlineSourceMap returns the build's JavaScript with its source map
// inlined, after removing the esbuild namespace from the map's file names so
// stack traces show "test.ts" rather than "loadtool:test.ts".
func withInlineSourceMap(files []api.OutputFile) (string, error) {
	var code, srcMap []byte
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".map") {
			srcMap = f.Contents
		} else {
			code = f.Contents
		}
	}
	if code == nil || srcMap == nil {
		return "", fmt.Errorf("expected code and source map, got %d output files", len(files))
	}

	var m map[string]any
	if err := json.Unmarshal(srcMap, &m); err != nil {
		return "", fmt.Errorf("read source map: %w", err)
	}
	if sources, ok := m["sources"].([]any); ok {
		for i, s := range sources {
			if name, ok := s.(string); ok {
				sources[i] = strings.TrimPrefix(name, scriptNamespace+":")
			}
		}
	}
	srcMap, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("write source map: %w", err)
	}

	// Replace esbuild's reference to the external map file.
	js := string(code)
	if i := strings.LastIndex(js, "//# sourceMappingURL="); i >= 0 {
		js = js[:i]
	}
	return js + "//# sourceMappingURL=data:application/json;base64," +
		base64.StdEncoding.EncodeToString(srcMap) + "\n", nil
}

// scriptPlugin serves the script's source from memory for the entry's
// import and rejects every other import. It records which protocol
// modules of mods (which may be nil) the script imports.
func scriptPlugin(filename, dir, src string, loader api.Loader, mods *moduleSet) api.Plugin {
	return api.Plugin{
		Name: "loadtool-script",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				switch {
				case args.Importer == "<stdin>" && args.Path == scriptImport:
					return api.OnResolveResult{Path: filename, Namespace: scriptNamespace}, nil
				case isBuiltinModule(args.Path):
					if i, ok := mods.index(args.Path); ok {
						mods.markImported(i)
					}
					return api.OnResolveResult{Path: args.Path, Namespace: builtinNamespace}, nil
				case isRelativeImport(args.Path):
					if dir == "" {
						return api.OnResolveResult{}, fmt.Errorf("relative imports need the script to be loaded from a file (importing %q)", args.Path)
					}
					// Let esbuild resolve it from the importing file's directory.
					return api.OnResolveResult{}, nil
				}
				return api.OnResolveResult{}, fmt.Errorf(
					"cannot import %q: only built-in modules (%s) and relative paths such as \"./helpers.ts\" can be imported",
					args.Path, strings.Join(mods.names(), ", "))
			})
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: scriptNamespace}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
				return api.OnLoadResult{Contents: &src, Loader: loader, ResolveDir: dir}, nil
			})
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: builtinNamespace}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				if i, ok := mods.index(args.Path); ok {
					mod := mods.source(i)
					return api.OnLoadResult{Contents: &mod, Loader: api.LoaderJS}, nil
				}
				mod, err := builtinModuleSource(args.Path, mods)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				return api.OnLoadResult{Contents: &mod, Loader: api.LoaderJS}, nil
			})
		},
	}
}

func buildError(filename string, msgs []api.Message) error {
	errs := make([]error, 0, len(msgs))
	for _, m := range msgs {
		l := m.Location
		switch {
		case l != nil:
			file := strings.TrimPrefix(l.File, scriptNamespace+":")
			errs = append(errs, fmt.Errorf("%s:%d:%d: %s", file, l.Line, l.Column+1, m.Text))
		default:
			errs = append(errs, fmt.Errorf("%s: %s", filename, m.Text))
		}
	}
	return errors.Join(errs...)
}

// errStopped is the interrupt value used when the test context ends.
var errStopped = errors.New("test stopped")

// maxCallStackSize bounds JavaScript call depth in each VU. goja's default
// is effectively unlimited and its call stack lives on the heap, so runaway
// recursion would grow memory until the whole process runs out (measured:
// about 33 MB/s for a single VU).
//
// A VU that hits the limit keeps the stack's memory, so the limit sets the
// worst case for a runaway script. Measured per VU: 1,000 frames ~0.8 MB,
// 2,500 ~1.7 MB, 10,000 ~4.8 MB. 2,500 allows deep legitimate recursion
// while keeping 1,000 runaway VUs near 1.7 GB.
const maxCallStackSize = 2_500

// scriptErrorMessage describes a script error. goja's stack overflow error
// only names the frame where it happened, so the cause is added.
func scriptErrorMessage(err error) string {
	var so *goja.StackOverflowError
	if errors.As(err, &so) {
		return fmt.Sprintf("maximum call stack size of %d frames exceeded%s", maxCallStackSize, err.Error())
	}
	msg := err.Error()
	if strings.Contains(msg, "ReferenceError: http is not defined") {
		// Phase 0 scripts used a global http object (ADR-005 replaced it).
		msg += ` (http is a module now: add import http from "loadtool/http")`
	}
	return msg
}

// VU is one virtual user's JavaScript runtime. It must only be used by a
// single goroutine.
type VU struct {
	rt     *goja.Runtime
	fn     goja.Callable
	client *http.Client

	// ctx and rec are set only while Iterate runs. Go callbacks invoked by
	// the script run on the VU's goroutine, so they read them without locks.
	ctx context.Context
	rec *metrics.Recorder
	// iter is the number of the next iteration, exposed as __ITER.
	iter int64
	// id is the VU number, exposed as __VU.
	id int64
	// exec is the exported function this VU's iterations call.
	exec string
	// console receives console output; nil discards it.
	console io.Writer
	// discardBodies, keepCookies and warn come from the Program.
	discardBodies bool
	keepCookies   bool
	warn          *warner
	// jar is this VU's cookie jar; client sends through it (ADR-009).
	jar httpclient.Jar
	// data is this VU's copy of the setup data, passed to every iteration;
	// undefined without setup data.
	data goja.Value
	// mods are the program's protocol modules; insts are this VU's
	// instances of them (by module index, nil until first use), and
	// closers the resources its scripts registered (ADR-018 §9).
	mods    *moduleSet
	insts   []protocol.Instance
	closers []io.Closer
}

// NewVU is NewVUExec for the default export.
func (p *Program) NewVU(ctx context.Context, id int, client *http.Client) (*VU, error) {
	return p.NewVUExec(ctx, id, "default", client)
}

// NewVUExec creates a runtime for VU number id, runs the script's top-level
// (init) code once, and resolves the exported function exec, which each
// iteration calls ("default", or a name given to WithExecs). id is exposed as __VU:
// 1..N for VUs, 0 for the runtime that only reads options. client is shared
// between VUs; http.Client is safe for concurrent use. If ctx ends while
// the top-level code runs (for example on Ctrl+C), the code is interrupted
// and ctx's error is returned.
func (p *Program) NewVUExec(ctx context.Context, id int, exec string, client *http.Client) (*VU, error) {
	if exec != "default" && !slices.Contains(p.execs, exec) {
		return nil, fmt.Errorf("exec %q was not prepared with WithExecs", exec)
	}
	rt := goja.New()
	rt.SetMaxCallStackSize(maxCallStackSize)
	vu := &VU{rt: rt, id: int64(id), exec: exec, discardBodies: p.discardBodies, keepCookies: p.keepCookies, warn: p.warn, mods: p.mods}
	props := builtinProps
	if p.mods != nil {
		vu.insts = make([]protocol.Instance, len(p.mods.all))
		props = append(slices.Clip(builtinProps), p.mods.props...)
	}
	if client != nil {
		// Shares client's transport (and connection pool); keeps its own
		// cookies.
		vu.client = httpclient.WithJar(client, &vu.jar)
	}
	// A nil *lockedWriter must become a nil io.Writer, not a non-nil
	// interface holding a nil pointer.
	if p.console != nil {
		vu.console = p.console
	}

	// Built-in modules and console methods are built on first use, so a
	// VU only pays memory for what its script uses (see lazyObject).
	if err := errors.Join(
		rt.Set(builtinGlobal, vu.newLazyObject(props)),
		rt.Set("__ENV", rt.NewDynamicObject(&envObject{rt: rt, base: p.env})),
		rt.Set("__VU", id),
		rt.Set("__ITER", 0),
		rt.Set("console", vu.newLazyObject(consoleProps)),
	); err != nil {
		return nil, err
	}
	release := vu.interruptOn(ctx)
	_, err := rt.RunProgram(p.prog)
	release()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("script init: %w", context.Cause(ctx))
		}
		return nil, fmt.Errorf("script init: %s", scriptErrorMessage(err))
	}

	// The lifecycle runtime (VU 0) never iterates, so a script whose
	// scenarios only use named exec functions needs no default export;
	// the runner checks what the scenarios need.
	fn, ok := goja.AssertFunction(rt.Get(defaultExportGlobal))
	if !ok && id != 0 {
		if exec != "default" {
			return nil, fmt.Errorf("exec %q is not an exported function", exec)
		}
		return nil, ErrNoDefaultExport
	}
	vu.fn = fn
	vu.data, err = vu.parseData(p.setupData)
	if err != nil {
		return nil, err
	}
	return vu, nil
}

// Options runs the script's top-level code once in a runtime of its own and
// returns its `export const options` as JSON, or nil if the script has no
// options. Callers that also run setup and teardown use NewLifecycle.
func (p *Program) Options(ctx context.Context) ([]byte, error) {
	l, err := p.NewLifecycle(ctx)
	if err != nil {
		return nil, err
	}
	return l.Options()
}

// Iterate calls the script's default function once. HTTP requests made by
// the script are recorded in rec. A script error ends the iteration and is
// recorded in rec; it never stops the test. If ctx ends while the script is
// running, the script is interrupted and nothing further is recorded.
//
// Once ctx is done the VU must not be used again.
func (vu *VU) Iterate(ctx context.Context, rec *metrics.Recorder) {
	if ctx.Err() != nil {
		return
	}
	vu.ctx, vu.rec = ctx, rec
	if !vu.keepCookies {
		vu.jar.Reset() // each iteration is a new session
	}
	vu.beginIteration(rec)
	_ = vu.rt.Set("__ITER", vu.iter)
	vu.iter++
	stop := context.AfterFunc(ctx, func() { vu.rt.Interrupt(errStopped) })
	v, err := vu.fn(goja.Undefined(), vu.data)
	if err == nil {
		_, err = settle(v) // an async function's errors are in its Promise
	}
	stop()
	vu.ctx, vu.rec = nil, nil

	if err != nil && ctx.Err() == nil {
		rec.RecordScriptError(scriptErrorMessage(err))
	}
}

// isRelativeImport reports whether path is relative to the importing file.
func isRelativeImport(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}

var promiseType = reflect.TypeFor[*goja.Promise]()

// errNeverSettled is the error for an async function whose Promise is
// still pending when it returns: something it awaits never resolves.
var errNeverSettled = errors.New("the async function did not finish: it awaits something that never resolves " +
	"(LoadTool has no event loop, so await only works on values that are already available)")

// settle returns the outcome of a function the script exported. A
// non-Promise value is returned as is. An async function returns a
// Promise; goja runs its jobs before the call returns, so with no event
// loop (ADR-017) the Promise is already settled unless it awaits
// something that never resolves. A rejected Promise is the function's
// error, which would otherwise be lost and hide every failure.
func settle(v goja.Value) (goja.Value, error) {
	// ExportType reads the type without converting the object, so a
	// function returning a plain object pays nothing here.
	obj, ok := v.(*goja.Object)
	if !ok || obj.ExportType() != promiseType {
		return v, nil
	}
	p := obj.Export().(*goja.Promise)
	switch p.State() {
	case goja.PromiseStateFulfilled:
		return p.Result(), nil
	case goja.PromiseStateRejected:
		return nil, rejection(p.Result())
	default:
		return nil, errNeverSettled
	}
}

// rejection describes a Promise's rejection reason: an Error's stack when
// it has one (it names the script location), else its text.
func rejection(reason goja.Value) error {
	msg := reason.String()
	if o, ok := reason.(*goja.Object); ok {
		if stack := o.Get("stack"); isSet(stack) && stack.String() != "" {
			msg = stack.String()
		}
	}
	return errors.New(msg)
}
