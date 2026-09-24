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
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// Program is a compiled test script, safe to share between VUs.
type Program struct {
	prog *goja.Program
}

// Load reads and compiles the script at path.
func Load(path string) (*Program, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Compile(filepath.Base(path), src)
}

// Compile transpiles src to JavaScript (stripping TypeScript types and
// converting ES module exports to CommonJS) and compiles it for goja.
// Files ending in .ts are treated as TypeScript, everything else as
// JavaScript.
func Compile(filename string, src []byte) (*Program, error) {
	loader := api.LoaderJS
	if strings.EqualFold(filepath.Ext(filename), ".ts") {
		loader = api.LoaderTS
	}
	out := api.Transform(string(src), api.TransformOptions{
		Loader:     loader,
		Format:     api.FormatCommonJS,
		Target:     api.ES2017,
		Sourcefile: filename,
		// Inline source maps let goja report errors at .ts line numbers.
		Sourcemap: api.SourceMapInline,
	})
	if len(out.Errors) > 0 {
		return nil, transformError(filename, out.Errors)
	}

	prog, err := goja.Compile(filename, string(out.Code), true)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", filename, err)
	}
	return &Program{prog: prog}, nil
}

func transformError(filename string, msgs []api.Message) error {
	errs := make([]error, 0, len(msgs))
	for _, m := range msgs {
		if l := m.Location; l != nil {
			errs = append(errs, fmt.Errorf("%s:%d:%d: %s", l.File, l.Line, l.Column+1, m.Text))
		} else {
			errs = append(errs, fmt.Errorf("%s: %s", filename, m.Text))
		}
	}
	return errors.Join(errs...)
}

// errStopped is the interrupt value used when the test context ends.
var errStopped = errors.New("test stopped")

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
}

// NewVU creates a runtime, runs the script's top-level (init) code once, and
// resolves its default export. client is shared between VUs; http.Client is
// safe for concurrent use.
func (p *Program) NewVU(client *http.Client) (*VU, error) {
	rt := goja.New()
	vu := &VU{rt: rt, client: client}

	module := rt.NewObject()
	exports := rt.NewObject()
	if err := errors.Join(
		module.Set("exports", exports),
		rt.Set("module", module),
		rt.Set("exports", exports),
		rt.Set("http", vu.newHTTPModule()),
	); err != nil {
		return nil, err
	}

	if _, err := rt.RunProgram(p.prog); err != nil {
		return nil, fmt.Errorf("script init: %w", err)
	}

	fn, err := defaultExport(rt, module)
	if err != nil {
		return nil, err
	}
	vu.fn = fn
	return vu, nil
}

func defaultExport(rt *goja.Runtime, module *goja.Object) (goja.Callable, error) {
	exp := module.Get("exports")
	if exp == nil || goja.IsUndefined(exp) || goja.IsNull(exp) {
		return nil, errors.New("script has no exports; add `export default function () { ... }`")
	}
	fn, ok := goja.AssertFunction(exp.ToObject(rt).Get("default"))
	if !ok {
		return nil, errors.New("script must export a default function: `export default function () { ... }`")
	}
	return fn, nil
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
	stop := context.AfterFunc(ctx, func() { vu.rt.Interrupt(errStopped) })
	_, err := vu.fn(goja.Undefined())
	stop()
	vu.ctx, vu.rec = nil, nil

	if err != nil && ctx.Err() == nil {
		rec.RecordScriptError(err.Error())
	}
}
