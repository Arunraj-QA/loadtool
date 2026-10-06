package script

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func lifecycle(t *testing.T, p *Program) *Lifecycle {
	t.Helper()
	l, err := p.NewLifecycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSetupDataReachesVUsAndTeardown(t *testing.T) {
	p := compile(t, "test.ts", `
let setupCalls = 0;
export function setup() {
	setupCalls++;
	if (__VU !== 0) throw new Error("setup ran in VU " + __VU);
	return { token: "abc", n: 1, fn: () => 1 };
}
export default function (data: any) {
	if (data.token !== "abc" || data.fn !== undefined) throw new Error("data " + JSON.stringify(data));
	data.n++; // a VU's changes stay in that VU
	(globalThis as any).seen = data.n;
}
export function teardown(data: any) {
	if (setupCalls !== 1) throw new Error("setup ran " + setupCalls + " times");
	if (data.token !== "abc" || data.n !== 1) throw new Error("teardown data " + JSON.stringify(data));
}`)
	l := lifecycle(t, p)
	data, err := l.Setup(context.Background(), http.DefaultClient, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"token":"abc","n":1}` {
		t.Fatalf("setup data = %s", got)
	}

	vp := p.WithSetupData(data)
	a, b := newVU(t, vp), newVU(t, vp)
	for range 3 {
		if s := iterate(a); s.ScriptErrors != 0 {
			t.Fatal(s.FirstScriptError)
		}
	}
	iterate(b)
	if got := a.rt.Get("seen").ToInteger(); got != 4 {
		t.Errorf("VU a saw n = %d, want 4 (its own copy, kept across iterations)", got)
	}
	if got := b.rt.Get("seen").ToInteger(); got != 2 {
		t.Errorf("VU b saw n = %d, want 2 (unaffected by VU a)", got)
	}
	if err := l.Teardown(context.Background(), http.DefaultClient, time.Minute, data); err != nil {
		t.Fatal(err)
	}
}

func TestNoSetupMeansUndefinedData(t *testing.T) {
	for _, tt := range []struct{ name, setup string }{
		{"no setup", ""},
		{"setup returns nothing", "export function setup() {}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := compile(t, "test.ts", tt.setup+`
export default function (data: any) { if (data !== undefined) throw new Error("data " + data); }
export function teardown(data: any) { if (data !== undefined) throw new Error("teardown data " + data); }`)
			l := lifecycle(t, p)
			data, err := l.Setup(context.Background(), http.DefaultClient, time.Minute)
			if err != nil || data != nil {
				t.Fatalf("Setup = %q, %v; want nil data", data, err)
			}
			if s := iterate(newVU(t, p.WithSetupData(data))); s.ScriptErrors != 0 {
				t.Fatal(s.FirstScriptError)
			}
			if err := l.Teardown(context.Background(), http.DefaultClient, time.Minute, data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetupFailures(t *testing.T) {
	tests := []struct {
		name, setup string
		timeout     time.Duration
		want        string
	}{
		{"throws", `export function setup() {
	throw new Error("login failed");
}`, time.Minute, "setup: Error: login failed at setup (test.ts:2:"},
		{"data has a cycle", `export function setup() { const a: any = {}; a.self = a; return a; }`,
			time.Minute, "setup: the returned data must be JSON-serializable"},
		{"times out", `export function setup() { for (;;) {} }`,
			50 * time.Millisecond, "setup did not finish within setupTimeout (setupTimeout 50ms)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := lifecycle(t, compile(t, "test.ts", tt.setup+"\nexport default function () {}"))
			_, err := l.Setup(context.Background(), http.DefaultClient, tt.timeout)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestSetupInterrupted(t *testing.T) {
	l := lifecycle(t, compile(t, "test.ts", `export function setup() { for (;;) {} }
export default function () {}`))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	_, err := l.Setup(ctx, http.DefaultClient, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "setup interrupted: context canceled") {
		t.Fatalf("error = %v, want setup interrupted", err)
	}
}

func TestTeardownFailures(t *testing.T) {
	tests := []struct {
		name, teardown string
		timeout        time.Duration
		want           string
	}{
		{"throws", `export function teardown() { throw new Error("cleanup failed"); }`,
			time.Minute, "teardown: Error: cleanup failed"},
		{"times out", `export function teardown() { for (;;) {} }`,
			50 * time.Millisecond, "teardown did not finish within teardownTimeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := lifecycle(t, compile(t, "test.ts", tt.teardown+"\nexport default function () {}"))
			err := l.Teardown(context.Background(), http.DefaultClient, tt.timeout, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// Requests and checks in setup and teardown run normally but are recorded
// nowhere: the VUs' recorders never see them. sleep is allowed.
func TestSetupCanUseHTTPChecksAndSleep(t *testing.T) {
	src, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"t-1"}`))
	}, `import http from "loadtool/http";
import { check, sleep } from "loadtool";
export const options = { discardResponseBodies: true };
export function setup() {
	const res = http.post("BASE_URL/login", "{}");
	check(res, { "logged in": (r) => r.status === 200 });
	sleep(0.01);
	return { token: res.json().token }; // bodies are kept in setup
}
export default function () {}`)
	p := compile(t, "test.ts", src).WithDiscardResponseBodies(true)
	data, err := lifecycle(t, p).Setup(context.Background(), http.DefaultClient, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"t-1"}` {
		t.Errorf("data = %s", data)
	}
}

// A setup that ends exactly as its timeout fires must not leave the
// runtime interrupted, or teardown would fail at once.
func TestInterruptIsClearedAfterRelease(t *testing.T) {
	vu := newVU(t, compile(t, "test.ts", "export default function () {}"))
	ctx, cancel := context.WithCancel(context.Background())
	release := vu.interruptOn(ctx)
	cancel() // the interrupt fires after the JavaScript has returned
	release()
	if _, err := vu.rt.RunString("1 + 1"); err != nil {
		t.Fatalf("runtime still interrupted after release: %v", err)
	}
}

func TestLifecycleKeepsVU0State(t *testing.T) {
	// setup and teardown share the VU-0 runtime and its module state.
	p := compile(t, "test.ts", `
let created = "";
export function setup() { created = "resource-1"; }
export default function () {}
export function teardown() { if (created !== "resource-1") throw new Error("lost state: " + created); }`)
	l := lifecycle(t, p)
	if _, err := l.Setup(context.Background(), http.DefaultClient, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := l.Teardown(context.Background(), http.DefaultClient, time.Minute, nil); err != nil {
		t.Fatal(err)
	}
}

// Only the lifecycle runtime (VU 0) keeps references to setup and
// teardown; every other VU skips them, so exporting them costs VUs no
// memory.
func TestVUsDoNotHoldLifecycleFunctions(t *testing.T) {
	p := compile(t, "test.ts", `export function setup() {}
export function teardown() {}
export default function () {}`)
	if l := lifecycle(t, p); !l.HasSetup() || !l.HasTeardown() {
		t.Fatal("lifecycle runtime does not see setup and teardown")
	}
	vu := newVU(t, p)
	for _, g := range []string{setupGlobal, teardownGlobal} {
		if v := vu.rt.Get(g); v != nil && !goja.IsUndefined(v) {
			t.Errorf("VU 1 has global %s", g)
		}
	}
}

// Cancelling the test while setup runs interrupts it even when setup then
// returns normally (sleep returns early when the test ends), so the
// runner never starts VUs after Ctrl+C.
func TestSetupCancelledButReturnedIsInterrupted(t *testing.T) {
	l := lifecycle(t, compile(t, "test.ts", `import { sleep } from "loadtool";
export function setup() { sleep(60); return { ok: true }; }
export default function () {}`))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	for range 20 { // the race between return and interrupt is timing dependent
		_, err := l.Setup(ctx, http.DefaultClient, time.Minute)
		if err == nil || !strings.Contains(err.Error(), "setup interrupted") {
			t.Fatalf("error = %v, want setup interrupted", err)
		}
	}
}
