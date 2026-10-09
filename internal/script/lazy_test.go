package script

import (
	"strings"
	"testing"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/protocols/graphql"
	"github.com/Arunraj-QA/loadtool/internal/protocols/grpc"
	"github.com/Arunraj-QA/loadtool/internal/protocols/kafka"
	"github.com/Arunraj-QA/loadtool/internal/protocols/ws"
)

func TestConsoleObjectBehavesLikeAnObject(t *testing.T) {
	var out strings.Builder
	p := compile(t, "test.ts", `
export default function () {
	const keys = Object.keys(console).join(",");
	if (keys !== "log,info,warn,error,debug") throw new Error("keys " + keys);
	if (typeof console.warn !== "function") throw new Error("warn is not a function");
	if (console.warn !== console.warn) throw new Error("warn is rebuilt on every access");
	if ("table" in console) throw new Error("unexpected console.table");

	let got = "";
	console.log = (msg: string) => { got = msg; };
	console.log("replaced");
	if (got !== "replaced") throw new Error("console.log could not be replaced");
}`).WithConsole(&out)
	if s := iterate(newVU(t, p)); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	if out.Len() != 0 {
		t.Errorf("replaced console.log still wrote %q", out.String())
	}
}

// A property is built once, on first access, and only if it is accessed:
// VUs do not pay memory for built-ins their script does not use.
func TestLazyObjectBuildsOnFirstAccess(t *testing.T) {
	builds := map[string]int{}
	prop := func(name string) lazyProp {
		return lazyProp{name, func(vu *VU) goja.Value {
			builds[name]++
			return vu.rt.ToValue(name + "-value")
		}}
	}
	vu := &VU{rt: goja.New()}
	if err := vu.rt.Set("o", vu.newLazyObject([]lazyProp{prop("a"), prop("b")})); err != nil {
		t.Fatal(err)
	}

	v, err := vu.rt.RunString(`[o.a, o.a, "b" in o, Object.keys(o).join(",")].join(" ")`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "a-value a-value true a,b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if builds["a"] != 1 || builds["b"] != 0 {
		t.Errorf("builds = %v, want a built once and b never", builds)
	}
}

// A registered module the script does not import adds nothing to its VUs
// (Phase 2 exit criterion 3): the builtin object has only the core
// properties and the VU no module instance slots. An imported module adds
// its own property only.
func TestUnimportedModulesCostVUsNothing(t *testing.T) {
	mods := []protocol.Module{ws.Module{}, grpc.Module{}, graphql.Module{}, kafka.Module{}}
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`import http from "loadtool/http"; export default function () { if (!http) throw 1; }`, []string{"http", "core", "exec"}},
		{`import ws from "loadtool/ws"; export default function () { if (!ws) throw 1; }`, []string{"http", "core", "exec", "ws"}},
	} {
		p, err := Compile("test.ts", []byte(tc.src), mods...)
		if err != nil {
			t.Fatal(err)
		}
		vu := newVU(t, p)
		keys := vu.rt.Get(builtinGlobal).ToObject(vu.rt).Keys()
		if strings.Join(keys, ",") != strings.Join(tc.want, ",") {
			t.Errorf("builtin properties %v; want %v", keys, tc.want)
		}
		if imported := len(tc.want) > 3; (vu.insts != nil) != imported {
			t.Errorf("module instance slots allocated: %v; want %v", vu.insts != nil, imported)
		}
	}
}
