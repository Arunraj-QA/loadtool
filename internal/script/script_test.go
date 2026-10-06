package script

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

func compile(t *testing.T, filename, src string) *Program {
	t.Helper()
	p, err := Compile(filename, []byte(src))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

func newVU(t *testing.T, p *Program) *VU {
	t.Helper()
	vu, err := p.NewVU(context.Background(), 1, httpclient.New(1, httpclient.DefaultTimeout))
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	return vu
}

// iterate runs one iteration and returns the merged metrics.
func iterate(vu *VU) metrics.Summary {
	rec := &metrics.Recorder{}
	vu.Iterate(context.Background(), rec)
	return metrics.Merge([]*metrics.Recorder{rec})
}

// server returns a test server and substitutes its URL for BASE_URL in src.
func server(t *testing.T, h http.HandlerFunc, src string) (string, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.ReplaceAll(src, "BASE_URL", srv.URL), srv
}

func ok(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }

func TestCompileTypeScript(t *testing.T) {
	src := `
interface Target { url: string }
const target: Target = { url: "unused" };
export default function (): void {
	const n: number = 1;
}
`
	vu := newVU(t, compile(t, "test.ts", src))
	if s := iterate(vu); s.ScriptErrors != 0 {
		t.Fatalf("unexpected script error: %s", s.FirstScriptError)
	}
}

func TestCompileJavaScript(t *testing.T) {
	vu := newVU(t, compile(t, "test.js", `export default function () {}`))
	if s := iterate(vu); s.ScriptErrors != 0 {
		t.Fatalf("unexpected script error: %s", s.FirstScriptError)
	}
}

func TestCompileSyntaxError(t *testing.T) {
	_, err := Compile("bad.ts", []byte("export default function ( {\n"))
	if err == nil || !strings.Contains(err.Error(), "bad.ts:2:1") {
		t.Fatalf("error = %v, want a bad.ts:2:1 location", err)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.ts")
	if err := os.WriteFile(path, []byte("export default function () {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.ts")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestLoadErrors covers scripts that must be rejected before load starts,
// either when compiling or when a VU runs the top-level code.
func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"no default export", `export const x = 1;`, "must export a default function"},
		{"default not a function", `export default 42;`, "must export a default function"},
		{"commonjs without default", `module.exports = undefined;`, "must export a default function"},
		{"top-level throw", `throw new Error("init failed"); export default function () {}`, "init failed"},
		{"request in init", `import http from "loadtool/http";
http.get("http://127.0.0.1:1/"); export default function () {}`, "not allowed in the script's top-level code"},
		{"relative import without a file", `import { f } from "./other"; export default function () { f(); }`, `relative imports need the script to be loaded from a file (importing "./other")`},
		{"package import", `import _ from "lodash"; export default function () { _(); }`, `cannot import "lodash"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Compile("test.ts", []byte(tt.src))
			if err == nil {
				_, err = p.NewVU(context.Background(), 1, http.DefaultClient)
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestTranspileHasNoInteropHelpers guards per-VU memory: esbuild's
// CommonJS interop helpers cost about 22 KB in every VU runtime.
func TestTranspileHasNoInteropHelpers(t *testing.T) {
	code, err := transpile("test.ts", "", []byte(`
let count: number = 0;
export default function (): void { count++; }`))
	if err != nil {
		t.Fatal(err)
	}
	for _, helper := range []string{"__export", "__copyProps", "__toCommonJS", "__toESM"} {
		if strings.Contains(code, helper) {
			t.Errorf("output contains %s:\n%s", helper, code)
		}
	}
}

func TestCompileErrorLocationHasNoNamespace(t *testing.T) {
	_, err := Compile("bad.ts", []byte("export default function ( {\n"))
	if err == nil || strings.Contains(err.Error(), scriptNamespace+":") {
		t.Fatalf("error = %v, want a location without the %q prefix", err, scriptNamespace)
	}
}

func TestHTTPGetReturnsStatusAndTimings(t *testing.T) {
	src, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
	}, `import http from "loadtool/http";

export default function () {
	const res = http.get("BASE_URL");
	if (res.status !== 418) throw new Error("status " + res.status);
	if (res.error !== "") throw new Error("error " + res.error);
	// Allow for coarse clocks (Windows ticks every ~0.5ms).
	if (!(res.timings.duration >= 15)) throw new Error("duration " + res.timings.duration);
}`)
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	if s.Requests != 1 || s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failed (418) request", s)
	}
}

func TestHTTPRequestSendsMethodBodyAndHeaders(t *testing.T) {
	type seen struct{ method, body, header string }
	got := make(chan seen, 1)
	src, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, string(b), r.Header.Get("Content-Type")}
		w.WriteHeader(http.StatusCreated)
	}, `import http from "loadtool/http";

export default function () {
	const res = http.request("post", "BASE_URL", JSON.stringify({ a: 1 }), {
		headers: { "Content-Type": "application/json" },
	});
	if (res.status !== 201) throw new Error("status " + res.status);
}`)
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 0 || s.Successes != 1 {
		t.Fatalf("got %+v", s)
	}
	want := seen{http.MethodPost, `{"a":1}`, "application/json"}
	if g := <-got; g != want {
		t.Errorf("server saw %+v, want %+v", g, want)
	}
}

func TestHTTPTransportErrorDoesNotThrow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(ok))
	url := srv.URL
	srv.Close()

	src := strings.ReplaceAll(`import http from "loadtool/http";

export default function () {
	const res = http.get("BASE_URL");
	if (res.status !== 0) throw new Error("status " + res.status);
	if (res.error === "") throw new Error("expected an error message");
}`, "BASE_URL", url)
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	if s.Requests != 1 || s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failed request", s)
	}
}

func TestScriptErrorIsRecordedNotFatal(t *testing.T) {
	src := `
export default function () {
	const x: number = 1;
	throw new Error("boom");
}`
	vu := newVU(t, compile(t, "test.ts", src))
	rec := &metrics.Recorder{}
	vu.Iterate(context.Background(), rec)
	vu.Iterate(context.Background(), rec) // the VU is still usable
	s := metrics.Merge([]*metrics.Recorder{rec})

	if s.ScriptErrors != 2 {
		t.Fatalf("ScriptErrors = %d, want 2", s.ScriptErrors)
	}
	// The source map should point at the original TypeScript line.
	if !strings.Contains(s.FirstScriptError, "boom") || !strings.Contains(s.FirstScriptError, "(test.ts:4:") {
		t.Errorf("first error %q should mention boom at (test.ts:4:", s.FirstScriptError)
	}
}

func TestHTTPArgumentErrors(t *testing.T) {
	tests := []struct {
		name, src, wantErr string
	}{
		{"get without url", `import http from "loadtool/http";
export default function () { http.get(); }`, "url is required"},
		{"request without method", `import http from "loadtool/http";
export default function () { http.request(); }`, "method is required"},
		{"request without url", `import http from "loadtool/http";
export default function () { http.request("GET"); }`, "url is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := iterate(newVU(t, compile(t, "test.js", tt.src)))
			if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, tt.wantErr) {
				t.Fatalf("got %d errors, first %q; want 1 containing %q",
					s.ScriptErrors, s.FirstScriptError, tt.wantErr)
			}
		})
	}
}

func TestVUsHaveIsolatedState(t *testing.T) {
	p := compile(t, "test.ts", `
let count = 0;
export default function () { count++; (globalThis as any).count = count; }`)
	a, b := newVU(t, p), newVU(t, p)
	for range 3 {
		iterate(a)
	}
	iterate(b)

	if got := a.rt.Get("count").ToInteger(); got != 3 {
		t.Errorf("VU a count = %d, want 3", got)
	}
	if got := b.rt.Get("count").ToInteger(); got != 1 {
		t.Errorf("VU b count = %d, want 1 (state leaked between VUs)", got)
	}
}

// Top-level declarations are module-scoped, as in an ES module: they do
// not become properties of globalThis. Besides matching ES module rules,
// this keeps them out of the global object, which costs memory in every VU.
func TestTopLevelDeclarationsAreModuleScoped(t *testing.T) {
	p := compile(t, "test.ts", `
import http from "loadtool/http";
var v = 1;
let l = 2;
function f() {}
export default function () {
	for (const name of ["v", "l", "f", "http", "m", "get", "request"]) {
		if (name in globalThis) throw new Error(name + " leaked to globalThis");
	}
	if (v + l !== 3 || typeof f !== "function" || typeof http.get !== "function") {
		throw new Error("top-level bindings are not visible to the script");
	}
}`)
	if s := iterate(newVU(t, p)); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
}

func TestConcurrentVUs(t *testing.T) {
	src, _ := server(t, ok, `import http from "loadtool/http";

let n = 0;
export default function () {
	n++;
	const res = http.get("BASE_URL");
	if (res.status !== 200) throw new Error("status " + res.status);
}`)
	p := compile(t, "test.ts", src)
	client := httpclient.New(8, httpclient.DefaultTimeout)

	const vus, iterations = 8, 25
	recs := make([]*metrics.Recorder, vus)
	var wg sync.WaitGroup
	for i := range vus {
		vu, err := p.NewVU(context.Background(), 1, client)
		if err != nil {
			t.Fatal(err)
		}
		recs[i] = &metrics.Recorder{}
		wg.Go(func() {
			for range iterations {
				vu.Iterate(context.Background(), recs[i])
			}
		})
	}
	wg.Wait()

	s := metrics.Merge(recs)
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	if s.Successes != vus*iterations {
		t.Fatalf("Successes = %d, want %d", s.Successes, vus*iterations)
	}
}

func TestIterateInterruptsLongRunningScript(t *testing.T) {
	vu := newVU(t, compile(t, "test.js", `export default function () { for (;;) {} }`))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	rec := &metrics.Recorder{}
	done := make(chan struct{})
	go func() {
		vu.Iterate(ctx, rec)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("infinite loop was not interrupted")
	}
	if s := metrics.Merge([]*metrics.Recorder{rec}); s.ScriptErrors != 0 {
		t.Fatalf("interruption at test end must not count as a script error: %s", s.FirstScriptError)
	}
}

func TestIterateWithDoneContextDoesNothing(t *testing.T) {
	vu := newVU(t, compile(t, "test.js", `export default function () { throw new Error("ran"); }`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &metrics.Recorder{}
	vu.Iterate(ctx, rec)
	if s := metrics.Merge([]*metrics.Recorder{rec}); s.ScriptErrors != 0 {
		t.Fatal("script ran with a cancelled context")
	}
}

func BenchmarkNewVU(b *testing.B) {
	p, err := Compile("test.ts", []byte(`import http from "loadtool/http";
export default function () { http.get("http://localhost/"); }`))
	if err != nil {
		b.Fatal(err)
	}
	client := httpclient.New(1, httpclient.DefaultTimeout)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.NewVU(context.Background(), 1, client); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIterateEmpty(b *testing.B) {
	p, err := Compile("test.ts", []byte(`export default function () {}`))
	if err != nil {
		b.Fatal(err)
	}
	vu, err := p.NewVU(context.Background(), 1, http.DefaultClient)
	if err != nil {
		b.Fatal(err)
	}
	rec := &metrics.Recorder{}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		vu.Iterate(ctx, rec)
	}
}

func BenchmarkIterateHTTPGet(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(ok))
	defer srv.Close()
	src := strings.ReplaceAll(`import http from "loadtool/http";
export default function () { http.get("BASE_URL"); }`, "BASE_URL", srv.URL)
	p, err := Compile("test.ts", []byte(src))
	if err != nil {
		b.Fatal(err)
	}
	client := httpclient.New(1, httpclient.DefaultTimeout)
	defer client.CloseIdleConnections()
	vu, err := p.NewVU(context.Background(), 1, client)
	if err != nil {
		b.Fatal(err)
	}
	rec := &metrics.Recorder{}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		vu.Iterate(ctx, rec)
	}
}

func TestExamplesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "examples")
	ts, err := filepath.Glob(filepath.Join(dir, "*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	js, err := filepath.Glob(filepath.Join(dir, "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	paths := append(ts, js...)
	var loaded int
	for _, path := range paths {
		if strings.HasSuffix(path, ".d.ts") {
			continue
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			p, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			// As the runner does: init code and options in the lifecycle
			// runtime, then a VU, unless the example's scenarios only
			// run named functions.
			l, err := p.NewLifecycle(context.Background())
			if err != nil {
				t.Fatalf("NewLifecycle: %v", err)
			}
			if _, err := l.Options(); err != nil {
				t.Fatalf("Options: %v", err)
			}
			if !l.HasDefault() {
				return
			}
			if _, err := p.NewVU(context.Background(), 1, http.DefaultClient); err != nil {
				t.Fatalf("NewVU: %v", err)
			}
		})
		loaded++
	}
	if loaded == 0 {
		t.Fatal("no examples found")
	}
}

const recursionScript = `
let mode = "recurse";
function f(n: number): number { return f(n + 1) + 1; }
export default function () {
	if (mode === "recurse") {
		mode = "normal";
		f(0);
	}
}`

func TestRunawayRecursionIsAScriptError(t *testing.T) {
	vu := newVU(t, compile(t, "test.ts", recursionScript))
	rec := &metrics.Recorder{}
	vu.Iterate(context.Background(), rec) // overflows
	vu.Iterate(context.Background(), rec) // the VU must still work afterwards
	s := metrics.Merge([]*metrics.Recorder{rec})

	if s.ScriptErrors != 1 {
		t.Fatalf("ScriptErrors = %d, want 1 (overflow, then a clean iteration)", s.ScriptErrors)
	}
	for _, want := range []string{"maximum call stack size of 2500 frames exceeded", "test.ts:3"} {
		if !strings.Contains(s.FirstScriptError, want) {
			t.Errorf("error %q does not contain %q", s.FirstScriptError, want)
		}
	}
}

func TestStackOverflowCannotBeCaughtByScript(t *testing.T) {
	src := `
function f(): number { return f() + 1; }
export default function () {
	try { f(); } catch (e) { /* must not swallow the overflow */ }
}`
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "maximum call stack size") {
		t.Fatalf("got %d script errors, first %q; want the overflow recorded", s.ScriptErrors, s.FirstScriptError)
	}
}

func TestRecursionInInitFailsLoad(t *testing.T) {
	_, err := compile(t, "test.ts", `
function f(): number { return f() + 1; }
f();
export default function () {}`).NewVU(context.Background(), 1, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "maximum call stack size of 2500 frames exceeded") {
		t.Fatalf("error = %v, want a call stack size error", err)
	}
}

func TestDeepButFiniteRecursionAllowed(t *testing.T) {
	src := `
function depth(n: number): number { return n === 0 ? 0 : depth(n - 1) + 1; }
export default function () {
	if (depth(2000) !== 2000) throw new Error("wrong depth");
}`
	if s := iterate(newVU(t, compile(t, "test.ts", src))); s.ScriptErrors != 0 {
		t.Fatalf("2,000-deep recursion failed: %s", s.FirstScriptError)
	}
}

// TestNewVUInterruptsTopLevelCode makes Ctrl+C work during VU start-up: a
// script whose top-level code never finishes must stop when ctx ends.
func TestNewVUInterruptsTopLevelCode(t *testing.T) {
	p := compile(t, "test.js", "for (;;) {}\nexport default function () {}")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() {
		_, err := p.NewVU(ctx, 1, http.DefaultClient)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("top-level code was not interrupted by ctx")
	}
}

func TestOptions(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"with options", `export const options = { vus: 3, duration: "200ms" }; export default function () {}`, `{"vus":3,"duration":"200ms"}`},
		{"functions are dropped", `export const options = { vus: 2, f() {} }; export default function () {}`, `{"vus":2}`},
		{"computed from top-level code", `const n = 2 * 4; export const options = { vus: n }; export default function () {}`, `{"vus":8}`},
		{"no options", `export default function () {}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compile(t, "test.ts", tt.src).Options(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("Options() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOptionsErrors(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"top-level throw", `throw new Error("bad init"); export const options = {}; export default function () {}`, "bad init"},
		{"not serializable", `const o: any = {}; o.self = o; export const options = o; export default function () {}`, "options:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compile(t, "test.ts", tt.src).Options(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestBuiltinHTTPModuleImports(t *testing.T) {
	src, _ := server(t, ok, `
import http, { get, request } from "loadtool/http";
export default function () {
	if (http.get("BASE_URL").status !== 200) throw new Error("default import");
	if (get("BASE_URL").status !== 200) throw new Error("named get");
	if (request("GET", "BASE_URL").status !== 200) throw new Error("named request");
}`)
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 0 || s.Successes != 3 {
		t.Fatalf("got %d script errors (%s), %d successes; want 0 and 3", s.ScriptErrors, s.FirstScriptError, s.Successes)
	}
}

func TestUnknownBuiltinModule(t *testing.T) {
	_, err := Compile("test.ts", []byte(`import x from "loadtool/ws"; export default function () { x(); }`))
	if err == nil || !strings.Contains(err.Error(), `unknown module "loadtool/ws"`) || !strings.Contains(err.Error(), `"loadtool/http"`) {
		t.Fatalf("error = %v, want an unknown-module error listing the built-in modules", err)
	}
}

// TestGlobalHTTPRemovedWithHint covers the ADR-005 breaking change: Phase 0
// scripts used a global http object; they now get a hint to import it.
func TestGlobalHTTPRemovedWithHint(t *testing.T) {
	s := iterate(newVU(t, compile(t, "test.js", `export default function () { http.get("http://127.0.0.1:1/"); }`)))
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, `import http from "loadtool/http"`) {
		t.Fatalf("got %d script errors, first %q; want one with the import hint", s.ScriptErrors, s.FirstScriptError)
	}
}

func TestBuiltinModulesAddNoInteropHelpers(t *testing.T) {
	code, err := transpile("test.ts", "", []byte(`
import http, { get } from "loadtool/http";
import lt from "loadtool";
export const options = { vus: 1 };
export default function () { http.get("x"); get("y"); return lt; }`))
	if err != nil {
		t.Fatal(err)
	}
	for _, helper := range []string{"__export", "__copyProps", "__toCommonJS", "__toESM"} {
		if strings.Contains(code, helper) {
			t.Errorf("output contains %s", helper)
		}
	}
}

// writeProject writes files (path relative to a temp dir) and returns the dir.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func relativeImportProject(t *testing.T, base string) string {
	return writeProject(t, map[string]string{
		"main.ts": `import http from "loadtool/http";
import { target, check200, boom } from "./lib/helpers.ts";
import cfg from "./config.json";
export const options = { vus: cfg.vus };
let n = 0;
export default function () {
	n++;
	if (n === 2) boom();
	check200(http.get(target()));
}
`,
		"lib/helpers.ts": `import { base } from "../shared";
export function target(): string { return base + "/x"; }
export function check200(res: { status: number }): void {
	if (res.status !== 200) throw new Error("bad status " + res.status);
}
export function boom(): never {
	throw new Error("helper boom");
}
`,
		"shared.ts":   `export const base: string = "` + base + `";` + "\n",
		"config.json": `{"vus": 3}`,
	})
}

func TestRelativeImports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(ok))
	t.Cleanup(srv.Close)
	dir := relativeImportProject(t, srv.URL)

	p, err := Load(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	opts, err := p.Options(context.Background())
	if err != nil || string(opts) != `{"vus":3}` {
		t.Fatalf("Options = %s, %v; want {\"vus\":3} from the imported JSON", opts, err)
	}

	vu, err := p.NewVU(context.Background(), 1, httpclient.New(1, httpclient.DefaultTimeout))
	if err != nil {
		t.Fatal(err)
	}
	rec := &metrics.Recorder{}
	vu.Iterate(context.Background(), rec) // request via helpers
	vu.Iterate(context.Background(), rec) // boom() in helpers.ts
	s := metrics.Merge([]*metrics.Recorder{rec})
	if s.Successes != 1 {
		t.Errorf("Successes = %d, want 1 request made through the helper module", s.Successes)
	}
	// The error must point at the imported file, at the throw on line 7.
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "helper boom") || !strings.Contains(s.FirstScriptError, "helpers.ts:7") {
		t.Errorf("got %d script errors, first %q; want helper boom at helpers.ts:7", s.ScriptErrors, s.FirstScriptError)
	}
}

func TestRelativeImportErrors(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"missing.ts":          `import { x } from "./nope"; export default function () { x(); }`,
		"package.ts":          `import { y } from "./lib/uses-package.ts"; export default function () { y(); }`,
		"lib/uses-package.ts": `import left from "left-pad"; export function y() { return left; }`,
	})
	tests := []struct{ file, want string }{
		{"missing.ts", `Could not resolve "./nope"`},
		{"package.ts", `cannot import "left-pad"`}, // also enforced inside imported files
	}
	for _, tt := range tests {
		_, err := Load(filepath.Join(dir, tt.file))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Load(%s) error = %v, want it to contain %q", tt.file, err, tt.want)
		}
	}
}

func TestRelativeImportsAddNoInteropHelpers(t *testing.T) {
	dir := relativeImportProject(t, "http://unused")
	src, err := os.ReadFile(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := transpile("main.ts", dir, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, helper := range []string{"__export", "__copyProps", "__toCommonJS", "__toESM"} {
		if strings.Contains(code, helper) {
			t.Errorf("output contains %s", helper)
		}
	}
}

func TestEnvObject(t *testing.T) {
	p := compile(t, "test.ts", `
export default function () {
	const want = (cond: boolean, what: string) => { if (!cond) throw new Error(what); };
	want(__ENV.A === "1", "read A");
	want("B" in __ENV, "B in __ENV");
	want(__ENV.C === undefined, "missing key is undefined");
	want(Object.keys(__ENV).join(",") === "A,B", "keys " + Object.keys(__ENV).join(","));
	want(JSON.stringify(__ENV) === '{"A":"1","B":"2"}', "json " + JSON.stringify(__ENV));
	__ENV.X = "9";
	delete __ENV.A;
	want(Object.keys(__ENV).join(",") === "B,X", "after write and delete: " + Object.keys(__ENV).join(","));
	want(__ENV.A === undefined && __ENV.X === "9", "values after write and delete");
}`).WithEnv(map[string]string{"A": "1", "B": "2"})
	if s := iterate(newVU(t, p)); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
}

func TestEnvIsolatedBetweenVUs(t *testing.T) {
	base := map[string]string{"A": "1"}
	p := compile(t, "test.ts", `
let writes = 0;
export default function () {
	if (writes++ === 0 && __VU === 1) { __ENV.A = "changed"; return; }
	if (__VU === 2 && __ENV.A !== "1") throw new Error("VU 2 saw " + __ENV.A);
}`).WithEnv(base)
	a, err := p.NewVU(context.Background(), 1, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewVU(context.Background(), 2, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	iterate(a)
	if s := iterate(b); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	if base["A"] != "1" {
		t.Fatalf("the shared map was modified: A=%q", base["A"])
	}
}

func TestVUAndIter(t *testing.T) {
	p := compile(t, "test.ts", `
export const initVU = __VU;
const seen: number[] = [];
(globalThis as any).seen = seen;
(globalThis as any).initVU = __VU;
export default function () {
	if (__VU !== 7) throw new Error("__VU " + __VU);
	seen.push(__ITER);
}`)
	vu, err := p.NewVU(context.Background(), 7, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if s := iterate(vu); s.ScriptErrors != 0 {
			t.Fatal(s.FirstScriptError)
		}
	}
	if got := vu.rt.Get("initVU").ToInteger(); got != 7 {
		t.Errorf("__VU in top-level code = %d, want 7", got)
	}
	if got := vu.rt.Get("seen").Export(); fmt.Sprint(got) != "[0 1 2]" {
		t.Errorf("__ITER values = %v, want [0 1 2]", got)
	}
}

func TestOptionsSeeVUZeroAndEnv(t *testing.T) {
	p := compile(t, "test.ts", `
export const options = { vus: __VU === 0 ? Number(__ENV.VUS) : 99 };
export default function () {}`).WithEnv(map[string]string{"VUS": "5"})
	got, err := p.Options(context.Background())
	if err != nil || string(got) != `{"vus":5}` {
		t.Fatalf("Options() = %s, %v; want {\"vus\":5} (__VU is 0 and __ENV is set while reading options)", got, err)
	}
}

func TestConsoleOutput(t *testing.T) {
	var buf bytes.Buffer
	p := compile(t, "test.ts", `
export default function () {
	console.log("hello", 42, true, undefined, null, { a: 1 }, [1, "x"]);
	console.info("i");
	console.warn("w");
	console.error("e");
	console.debug("d");
	const loop: any = {}; loop.self = loop;
	console.log(loop);
}`).WithConsole(&buf)
	vu, err := p.NewVU(context.Background(), 3, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if s := iterate(vu); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	want := `INFO  [VU 3] hello 42 true undefined null {"a":1} [1,"x"]
INFO  [VU 3] i
WARN  [VU 3] w
ERROR [VU 3] e
DEBUG [VU 3] d
INFO  [VU 3] [object Object]
`
	if buf.String() != want {
		t.Errorf("console output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestConsoleDiscardedWithoutWriter(t *testing.T) {
	// No WithConsole: console calls must work and write nowhere.
	s := iterate(newVU(t, compile(t, "test.js", `export default function () { console.log("x"); console.error("y"); }`)))
	if s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
}

func TestConsoleLinesNotInterleaved(t *testing.T) {
	var buf bytes.Buffer
	p := compile(t, "test.js", `
const msg = "x".repeat(200);
export default function () { for (let i = 0; i < 50; i++) console.log(msg); }`).WithConsole(&buf)
	const vus = 8
	var wg sync.WaitGroup
	for i := range vus {
		vu, err := p.NewVU(context.Background(), i+1, http.DefaultClient)
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() { iterate(vu) })
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != vus*50 {
		t.Fatalf("got %d lines, want %d", len(lines), vus*50)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "INFO  [VU ") || !strings.HasSuffix(l, "] "+strings.Repeat("x", 200)) {
			t.Fatalf("corrupted line: %q", l)
		}
	}
}

func TestSleep(t *testing.T) {
	vu := newVU(t, compile(t, "test.ts", `import { sleep } from "loadtool"; export default function () { sleep(0.05); }`))
	start := time.Now()
	if s := iterate(vu); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	// Allow for coarse clocks (Windows ticks every ~0.5ms).
	if took := time.Since(start); took < 45*time.Millisecond || took > 2*time.Second {
		t.Errorf("sleep(0.05) took %v, want about 50ms", took)
	}
}

func TestSleepStopsWhenRunEnds(t *testing.T) {
	vu := newVU(t, compile(t, "test.ts", `import lt from "loadtool"; export default function () { lt.sleep(3600); }`))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	rec := &metrics.Recorder{}
	start := time.Now()
	vu.Iterate(ctx, rec)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("sleep(3600) delayed the end of the run by %v", took)
	}
	if s := metrics.Merge([]*metrics.Recorder{rec}); s.ScriptErrors != 0 {
		t.Errorf("stopping a sleeping VU must not be a script error: %s", s.FirstScriptError)
	}
}

func TestSleepErrors(t *testing.T) {
	_, err := compile(t, "test.ts", `import { sleep } from "loadtool"; sleep(1); export default function () {}`).NewVU(context.Background(), 1, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "sleep is not allowed in the script's top-level code") {
		t.Errorf("sleep in top-level code: error = %v", err)
	}
	for _, arg := range []string{"-1", `"soon"`, "NaN", "Infinity", ""} {
		s := iterate(newVU(t, compile(t, "test.ts", `import { sleep } from "loadtool"; export default function () { sleep(`+arg+`); }`)))
		if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "sleep: seconds must be a non-negative number") {
			t.Errorf("sleep(%s): got %d errors, first %q", arg, s.ScriptErrors, s.FirstScriptError)
		}
	}
}

func TestGroup(t *testing.T) {
	s := iterate(newVU(t, compile(t, "test.ts", `
import lt, { group } from "loadtool";
export default function () {
	const v = group("outer", () => lt.group("inner", () => 41) + 1);
	if (v !== 42) throw new Error("group returned " + v);
	group("failing", () => {
		throw new Error("inside group");
	});
}`)))
	// The first group returns its value; the exception from the second keeps
	// its original location (line 7).
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "inside group") || !strings.Contains(s.FirstScriptError, "test.ts:7") {
		t.Fatalf("got %d errors, first %q; want the inner exception at test.ts:7", s.ScriptErrors, s.FirstScriptError)
	}
	s = iterate(newVU(t, compile(t, "test.js", `import { group } from "loadtool"; export default function () { group("no fn"); }`)))
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "second argument must be a function") {
		t.Fatalf("group without a function: got %q", s.FirstScriptError)
	}
}
