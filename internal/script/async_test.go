package script

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// An async default function returns a Promise; its errors are in the
// Promise. They must count as script errors, not vanish.
func TestAsyncDefaultFunctionErrorsAreReported(t *testing.T) {
	s := iterate(newVU(t, compile(t, "test.ts", `
export default async function () {
	throw new Error("boom");
}`)))
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "boom") || !strings.Contains(s.FirstScriptError, "test.ts") {
		t.Errorf("got %d script errors, first %q; want boom with its location", s.ScriptErrors, s.FirstScriptError)
	}
}

// await on values that are already available works, and checks in an
// async function count as usual.
func TestAsyncDefaultFunctionWithAwait(t *testing.T) {
	s := iterate(newVU(t, compile(t, "test.ts", `
import { check } from "loadtool";
export default async function () {
	const v = await Promise.resolve(41);
	check(v + 1, { "is 42": (x) => x === 42 });
}`)))
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	if len(s.Checks) != 1 || s.Checks[0].Passes != 1 {
		t.Errorf("checks = %+v, want one pass", s.Checks)
	}
}

// An async function that awaits something that never resolves cannot
// finish without an event loop (ADR-017): that is a script error that
// says so, not a silent no-op.
func TestAsyncDefaultFunctionThatNeverSettles(t *testing.T) {
	s := iterate(newVU(t, compile(t, "test.ts", `
export default async function () {
	await new Promise(() => {});
}`)))
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "no event loop") {
		t.Errorf("got %d script errors, first %q", s.ScriptErrors, s.FirstScriptError)
	}
}

// An async setup's resolved value is the setup data; its rejection is a
// setup error.
func TestAsyncSetup(t *testing.T) {
	p := compile(t, "test.ts", `
export async function setup() { return { token: await Promise.resolve("abc") }; }
export default function (data) { if (data.token !== "abc") throw new Error("data " + JSON.stringify(data)); }`)
	l, err := p.NewLifecycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := l.Setup(context.Background(), http.DefaultClient, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"abc"}` {
		t.Errorf("setup data = %s", data)
	}
	if s := iterate(newVU(t, p.WithSetupData(data))); s.ScriptErrors != 0 {
		t.Errorf("iteration: %s", s.FirstScriptError)
	}

	bad := compile(t, "test.ts", `
export async function setup() { throw new Error("no login"); }
export default function () {}`)
	l, err = bad.NewLifecycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Setup(context.Background(), http.DefaultClient, time.Second); err == nil || !strings.Contains(err.Error(), "setup: Error: no login") {
		t.Errorf("Setup error = %v, want the rejection", err)
	}
}
