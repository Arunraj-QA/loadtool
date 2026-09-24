package script

import (
	"context"
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
	vu, err := p.NewVU(httpclient.New(1, httpclient.DefaultTimeout))
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
		{"request in init", `http.get("http://127.0.0.1:1/"); export default function () {}`, "not allowed in the script's top-level code"},
		{"import", `import { f } from "./other"; export default function () { f(); }`, `imports are not supported yet (importing "./other")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Compile("test.ts", []byte(tt.src))
			if err == nil {
				_, err = p.NewVU(http.DefaultClient)
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
	code, err := transpile("test.ts", []byte(`
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
	}, `
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
	}, `
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

	src := strings.ReplaceAll(`
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
		{"get without url", `export default function () { http.get(); }`, "url is required"},
		{"request without method", `export default function () { http.request(); }`, "method is required"},
		{"request without url", `export default function () { http.request("GET"); }`, "url is required"},
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
export default function () { count++; }`)
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

func TestConcurrentVUs(t *testing.T) {
	src, _ := server(t, ok, `
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
		vu, err := p.NewVU(client)
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
	p, err := Compile("test.ts", []byte(`export default function () { http.get("http://localhost/"); }`))
	if err != nil {
		b.Fatal(err)
	}
	client := httpclient.New(1, httpclient.DefaultTimeout)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.NewVU(client); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIterateEmpty(b *testing.B) {
	p, err := Compile("test.ts", []byte(`export default function () {}`))
	if err != nil {
		b.Fatal(err)
	}
	vu, err := p.NewVU(http.DefaultClient)
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
	src := strings.ReplaceAll(`export default function () { http.get("BASE_URL"); }`, "BASE_URL", srv.URL)
	p, err := Compile("test.ts", []byte(src))
	if err != nil {
		b.Fatal(err)
	}
	client := httpclient.New(1, httpclient.DefaultTimeout)
	defer client.CloseIdleConnections()
	vu, err := p.NewVU(client)
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
			if _, err := p.NewVU(http.DefaultClient); err != nil {
				t.Fatalf("NewVU: %v", err)
			}
		})
		loaded++
	}
	if loaded == 0 {
		t.Fatal("no examples found")
	}
}
