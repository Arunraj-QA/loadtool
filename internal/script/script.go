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

const (
	// scriptImport is how the generated entry imports the user's script.
	scriptImport = "loadtool:script"
	// scriptNamespace is the esbuild namespace the script is loaded in;
	// esbuild prefixes file names with it, so it is stripped for users.
	scriptNamespace = "loadtool"
	// defaultExportGlobal is where the entry stores the default export.
	defaultExportGlobal = "__loadtool_default"
	// outfile names the in-memory build output; nothing is written to disk.
	outfile = "script.js"
)

// entrySource imports the script's default export and stores it in a
// global. Bundling this entry lets esbuild bind the default export
// directly, so the output needs no CommonJS interop helpers. Every VU runs
// the output, and those helpers made up about 75% of per-VU memory (see
// benchmarks/results/2026-09-24-vu-memory.md).
var entrySource = fmt.Sprintf("import fn from %q;\nglobalThis.%s = fn;\n", scriptImport, defaultExportGlobal)

var errNoDefaultExport = errors.New("script must export a default function: `export default function () { ... }`")

// Compile transpiles src to JavaScript (stripping TypeScript types and
// resolving the default export) and compiles it for goja. Files ending in
// .ts are treated as TypeScript, everything else as JavaScript. Imports are
// not supported.
func Compile(filename string, src []byte) (*Program, error) {
	code, err := transpile(filename, src)
	if err != nil {
		return nil, err
	}
	prog, err := goja.Compile(filename, code, true)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", filename, err)
	}
	return &Program{prog: prog}, nil
}

// transpile turns the script into plain JavaScript that stores its default
// export in defaultExportGlobal.
func transpile(filename string, src []byte) (string, error) {
	loader := api.LoaderJS
	if strings.EqualFold(filepath.Ext(filename), ".ts") {
		loader = api.LoaderTS
	}
	out := api.Build(api.BuildOptions{
		Stdin:    &api.StdinOptions{Contents: entrySource},
		Bundle:   true,
		Format:   api.FormatESModule,
		Platform: api.PlatformNeutral,
		Target:   api.ES2017,
		Plugins:  []api.Plugin{scriptPlugin(filename, string(src), loader)},
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
// import and rejects every other import.
func scriptPlugin(filename, src string, loader api.Loader) api.Plugin {
	return api.Plugin{
		Name: "loadtool-script",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if args.Importer == "<stdin>" && args.Path == scriptImport {
					return api.OnResolveResult{Path: filename, Namespace: scriptNamespace}, nil
				}
				return api.OnResolveResult{}, fmt.Errorf("imports are not supported yet (importing %q)", args.Path)
			})
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: scriptNamespace}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
				return api.OnLoadResult{Contents: &src, Loader: loader}, nil
			})
		},
	}
}

func buildError(filename string, msgs []api.Message) error {
	errs := make([]error, 0, len(msgs))
	for _, m := range msgs {
		l := m.Location
		switch {
		case l != nil && l.File == "<stdin>" && strings.Contains(m.Text, `for import "default"`):
			// The generated entry could not find a default export.
			errs = append(errs, fmt.Errorf("%s: %w", filename, errNoDefaultExport))
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

	if err := rt.Set("http", vu.newHTTPModule()); err != nil {
		return nil, err
	}
	if _, err := rt.RunProgram(p.prog); err != nil {
		return nil, fmt.Errorf("script init: %w", err)
	}

	fn, ok := goja.AssertFunction(rt.Get(defaultExportGlobal))
	if !ok {
		return nil, errNoDefaultExport
	}
	vu.fn = fn
	return vu, nil
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
