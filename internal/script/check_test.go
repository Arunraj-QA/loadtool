package script

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

func TestCheckRecordsEachCondition(t *testing.T) {
	src, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>not json</html>`))
	}, `import http from "loadtool/http";
import { check } from "loadtool";
export default function () {
	const res = http.get("BASE_URL");
	const all = check(res, {
		"status is 200": (r) => r.status === 200,
		"is json": (r) => r.json().id > 0,
		"status is 201": (r) => r.status === 201,
		"truthy value counts": (r) => r.body.length,
	});
	if (all !== false) throw new Error("check returned " + all);
	if (check(res, { "status is 200": (r) => r.status === 200 }) !== true) {
		throw new Error("an all-passing check did not return true");
	}
}`)
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	// A throwing condition fails its check without ending the iteration.
	if len(s.Checks) != 4 || !strings.Contains(s.Checks[1].FirstError, "SyntaxError") {
		t.Fatalf("Checks = %+v, want 4 with a SyntaxError on \"is json\"", s.Checks)
	}
	s.Checks[1].FirstError = ""
	want := []metrics.CheckResult{
		{Name: "status is 200", Passes: 2},
		{Name: "is json", Fails: 1},
		{Name: "status is 201", Fails: 1},
		{Name: "truthy value counts", Passes: 1},
	}
	if !reflect.DeepEqual(s.Checks, want) {
		t.Errorf("Checks = %+v, want %+v", s.Checks, want)
	}
}

func TestCheckMisuseIsAScriptError(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"no conditions", `check(1);`, "must be an object"},
		{"condition not a function", `check(1, { "x": true });`, `condition "x" must be a function`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := compile(t, "test.ts", `import { check } from "loadtool";
export default function () { `+tt.body+` }`)
			s := iterate(newVU(t, p))
			if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, tt.want) {
				t.Errorf("got %d errors, first %q; want one containing %q", s.ScriptErrors, s.FirstScriptError, tt.want)
			}
		})
	}
}

func TestCheckNotAllowedInTopLevelCode(t *testing.T) {
	p := compile(t, "test.ts", `import { check } from "loadtool";
check(1, { "x": () => true });
export default function () {}`)
	_, err := p.NewVU(context.Background(), 1, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "check is not allowed in the script's top-level code") {
		t.Fatalf("NewVU error = %v, want the top-level check error", err)
	}
}

// A run that ends while a condition is running stops the iteration; the
// interrupt is not counted as a failed check.
func TestCheckInterruptedIsNotAFailure(t *testing.T) {
	p := compile(t, "test.ts", `import { check } from "loadtool";
export default function () { check(1, { "spins": () => { for (;;) {} } }); }`)
	vu := newVU(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	rec := &metrics.Recorder{}
	vu.Iterate(ctx, rec)
	s := metrics.Merge([]*metrics.Recorder{rec})
	if len(s.Checks) != 0 || s.ScriptErrors != 0 {
		t.Errorf("Checks = %+v, ScriptErrors = %d; want neither after an interrupt", s.Checks, s.ScriptErrors)
	}
}

func TestCheckFromDefaultImport(t *testing.T) {
	p := compile(t, "test.ts", `import loadtool from "loadtool";
export default function () {
	if (!loadtool.check(2, { "is two": (v) => v === 2 })) throw new Error("failed");
}`)
	s := iterate(newVU(t, p))
	if s.ScriptErrors != 0 || len(s.Checks) != 1 || s.Checks[0].Passes != 1 {
		t.Fatalf("Checks = %+v, error %q", s.Checks, s.FirstScriptError)
	}
}

func BenchmarkCheck(b *testing.B) {
	p, err := Compile("test.ts", []byte(`import { check } from "loadtool";
const res = { status: 200 };
export default function () { check(res, { "status is 200": (r) => r.status === 200 }); }`))
	if err != nil {
		b.Fatal(err)
	}
	vu, err := p.NewVU(context.Background(), 1, nil)
	if err != nil {
		b.Fatal(err)
	}
	rec := metrics.NewRecorders(1)[0]
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		vu.Iterate(ctx, rec)
	}
}
