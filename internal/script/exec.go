package script

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// WithExecs returns a copy of p whose VUs can run the exported functions
// names, as scenarios' exec options name them (ADR-008), as well as the
// default export. Each name must be a named export of the script; it is
// an error otherwise, so a typo is caught before setup or any load.
//
// The script is compiled again with an entry that binds only these
// functions. Each VU then stores just the one it runs, chosen through the
// lazy "exec" built-in, so naming several adds no globals to every VU.
func (p *Program) WithExecs(names []string) (*Program, error) {
	var execs []string
	for _, n := range names {
		if n != "default" && !slices.Contains(execs, n) {
			execs = append(execs, n)
		}
	}
	if len(execs) == 0 {
		return p, nil
	}
	slices.Sort(execs)

	exports, err := exportNames(p.filename, p.dir, p.src)
	if err != nil {
		return nil, err
	}
	for _, n := range execs {
		if !slices.Contains(exports, n) {
			return nil, fmt.Errorf("exec %q is not exported by %s; export it with `export function %s() { ... }`", n, p.filename, n)
		}
	}

	code, err := transpileEntry(p.filename, p.dir, p.src, entryFor(execs), p.mods)
	if err != nil {
		return nil, err
	}
	prog, err := goja.Compile(p.filename, code, true)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", p.filename, err)
	}
	c := *p
	c.prog, c.execs = prog, execs
	return &c, nil
}

// entryFor is entrySource with the default-export slot chosen per VU:
//
//	const exec = globalThis.__loadtool_builtin.exec;
//	globalThis.__loadtool_default = exec === "a" ? mod.a : exec === "b" ? mod.b : mod.default;
//
// esbuild turns each mod.<name> into a direct reference, so the bundle
// still needs no interop helpers.
func entryFor(execs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "import * as mod from %q;\n", scriptImport)
	fmt.Fprintf(&b, "const exec = globalThis.%s.exec;\n", builtinGlobal)
	fmt.Fprintf(&b, "globalThis.%s = ", defaultExportGlobal)
	for _, n := range execs {
		fmt.Fprintf(&b, "exec === %q ? mod.%s : ", n, n)
	}
	b.WriteString("mod.default;\n")
	fmt.Fprintf(&b, "globalThis.%s = mod.options;\n", optionsGlobal)
	fmt.Fprintf(&b, "if (__VU === 0) { globalThis.%s = mod.setup; globalThis.%s = mod.teardown; }\n", setupGlobal, teardownGlobal)
	return b.String()
}

// exportNames lists the script's named exports, from esbuild's metadata
// for a bundle that re-exports them ("export *" leaves out default).
func exportNames(filename, dir string, src []byte) ([]string, error) {
	loader := api.LoaderJS
	if strings.EqualFold(filepath.Ext(filename), ".ts") {
		loader = api.LoaderTS
	}
	out := api.Build(api.BuildOptions{
		Stdin:         &api.StdinOptions{Contents: fmt.Sprintf("export * from %q;\n", scriptImport)},
		Bundle:        true,
		Format:        api.FormatESModule,
		Platform:      api.PlatformNeutral,
		Target:        api.ES2017,
		Plugins:       []api.Plugin{scriptPlugin(filename, dir, string(src), loader, nil)},
		AbsWorkingDir: dir,
		Outfile:       outfile,
		Metafile:      true,
		LogLevel:      api.LogLevelSilent,
	})
	if len(out.Errors) > 0 {
		return nil, buildError(filename, out.Errors)
	}
	var meta struct {
		Outputs map[string]struct {
			Exports []string `json:"exports"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(out.Metafile), &meta); err != nil {
		return nil, fmt.Errorf("read the exports of %s: %w", filename, err)
	}
	var names []string
	for _, o := range meta.Outputs {
		names = append(names, o.Exports...)
	}
	return names, nil
}
