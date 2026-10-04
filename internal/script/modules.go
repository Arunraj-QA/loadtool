package script

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// builtinGlobal holds the Go-backed module objects of a VU. The
	// built-in module sources below read from it.
	builtinGlobal = "__loadtool_builtin"
	// builtinNamespace is the esbuild namespace of built-in modules.
	builtinNamespace = "loadtool-builtin"
)

// builtinModules are the modules scripts import by name (ADR-005, ADR-007).
// Each is a small ES module that re-exports objects the VU's runtime
// provides, so esbuild binds the imports directly and adds no interop
// helpers.
var builtinModules = map[string]string{
	"loadtool/http": `const m = globalThis.` + builtinGlobal + `.http;
export default m;
export const get = m.get;
export const request = m.request;
`,
	"loadtool": `const m = globalThis.` + builtinGlobal + `.core;
export const sleep = m.sleep;
// group runs fn and returns its result. Tagging metrics with the group
// name comes with the metrics registry.
export function group(name, fn) {
	if (typeof fn !== "function") {
		throw new TypeError("group: the second argument must be a function");
	}
	return fn();
}
export default { sleep, group };
`,
}

// isBuiltinModule reports whether path names a built-in module or one in
// the "loadtool/" namespace (which may simply not exist).
func isBuiltinModule(path string) bool {
	return path == "loadtool" || strings.HasPrefix(path, "loadtool/")
}

// builtinModuleSource returns the source of a built-in module.
func builtinModuleSource(path string) (string, error) {
	src, ok := builtinModules[path]
	if !ok {
		names := make([]string, 0, len(builtinModules))
		for n := range builtinModules {
			names = append(names, fmt.Sprintf("%q", n))
		}
		sort.Strings(names)
		return "", fmt.Errorf("unknown module %q; built-in modules are %s", path, strings.Join(names, ", "))
	}
	return src, nil
}
