package script

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const execScript = `
export function browse(data: any) { (globalThis as any).ran = "browse:" + data.n; }
export function order(data: any) { (globalThis as any).ran = "order:" + data.n; }
export const notAFunction = 1;
export default function (data: any) { (globalThis as any).ran = "default:" + data.n; }`

func TestEachVURunsItsExec(t *testing.T) {
	p, err := compile(t, "test.ts", execScript).WithExecs([]string{"order", "default", "browse", "order"})
	if err != nil {
		t.Fatal(err)
	}
	p = p.WithSetupData([]byte(`{"n": 7}`))
	for _, exec := range []string{"default", "browse", "order"} {
		vu, err := p.NewVUExec(context.Background(), 1, exec, http.DefaultClient)
		if err != nil {
			t.Fatal(err)
		}
		if s := iterate(vu); s.ScriptErrors != 0 {
			t.Fatal(s.FirstScriptError)
		}
		if got, want := vu.rt.Get("ran").String(), exec+":7"; got != want {
			t.Errorf("exec %s ran %q, want %q", exec, got, want)
		}
	}
}

func TestWithExecsErrors(t *testing.T) {
	p := compile(t, "test.ts", execScript)
	if _, err := p.WithExecs([]string{"chekout"}); err == nil || !strings.Contains(err.Error(), `exec "chekout" is not exported by test.ts`) {
		t.Errorf("missing export: error = %v", err)
	}
	q, err := p.WithExecs([]string{"notAFunction"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.NewVUExec(context.Background(), 1, "notAFunction", http.DefaultClient); err == nil || !strings.Contains(err.Error(), `exec "notAFunction" is not an exported function`) {
		t.Errorf("non-function export: error = %v", err)
	}
	if _, err := p.NewVUExec(context.Background(), 1, "browse", http.DefaultClient); err == nil || !strings.Contains(err.Error(), "not prepared with WithExecs") {
		t.Errorf("unprepared exec: error = %v", err)
	}
}

// Only "default" (or nothing) leaves the program as it is.
func TestWithExecsDefaultOnlyIsANoOp(t *testing.T) {
	p := compile(t, "test.ts", execScript)
	if q, err := p.WithExecs([]string{"default"}); err != nil || q != p {
		t.Errorf("WithExecs(default) = %p, %v; want the same program", q, err)
	}
}

// The entry with execs still binds exports directly: no interop helpers,
// which would cost every VU memory.
func TestExecEntryHasNoInteropHelpers(t *testing.T) {
	code, err := transpileEntry("test.ts", "", []byte(execScript), entryFor([]string{"browse", "order"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, helper := range []string{"__export", "__copyProps", "__toCommonJS", "__toESM"} {
		if strings.Contains(code, helper) {
			t.Errorf("bundle contains %s:\n%s", helper, code)
		}
	}
}

// A script whose scenarios use only named functions needs no default
// export: the lifecycle runtime does not require one.
func TestLifecycleWithoutDefaultExport(t *testing.T) {
	p := compile(t, "test.ts", `export function api() {}`)
	l, err := p.NewLifecycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.HasDefault() {
		t.Error("HasDefault = true, want false")
	}
	if _, err := p.NewVU(context.Background(), 1, http.DefaultClient); err == nil || !strings.Contains(err.Error(), "must export a default function") {
		t.Errorf("a VU running default still needs it: error = %v", err)
	}
}
