package script

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// runScript compiles src, runs one iteration in a new VU and fails the
// test if the iteration threw.
func runScript(t *testing.T, p *Program) {
	t.Helper()
	if s := iterate(newVU(t, p)); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
}

func TestResponseFields(t *testing.T) {
	src, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id": 7, "tags": ["x"]}`)
	}, `import http from "loadtool/http";
export default function () {
	const res = http.get("BASE_URL/items");
	const want = (cond: boolean, what: string) => { if (!cond) throw new Error(what); };
	want(res.status === 201, "status " + res.status);
	want(res.error === "", "error " + res.error);
	want(res.url === "BASE_URL/items", "url " + res.url);
	want(res.headers["Content-Type"] === "application/json", "content-type " + res.headers["Content-Type"]);
	want(res.headers["X-Multi"] === "a, b", "joined header " + res.headers["X-Multi"]);
	want(res.body === '{"id": 7, "tags": ["x"]}', "body " + res.body);
	want(res.json().id === 7 && res.json().tags[0] === "x", "json");
	want(typeof res.timings.duration === "number" && res.timings.duration >= 0, "duration");
	want(res.headers === res.headers && res.body === res.body, "values are cached");
	want(Object.keys(res).join(",") === "status,error,headers,body,timings,url,cookies", "keys " + Object.keys(res));
	want(JSON.parse(JSON.stringify(res)).status === 201, "stringify");

	res.note = "added";
	want(res.note === "added", "scripts can add properties");
}`)
	runScript(t, compile(t, "test.ts", src))
}

func TestHTTPMethodsSendBodyAndHeaders(t *testing.T) {
	type seen struct{ method, body, header string }
	var (
		mu  sync.Mutex
		got []seen
	)
	src, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{r.Method, string(b), r.Header.Get("X-Test")})
		mu.Unlock()
	}, `import http, { post } from "loadtool/http";
const p = { headers: { "X-Test": "yes" } };
export default function () {
	post("BASE_URL", "1", p);
	http.put("BASE_URL", "2", p);
	http.patch("BASE_URL", "3", p);
	http.del("BASE_URL", null, p);
	http.request("options", "BASE_URL", "5", p);
	http.get("BASE_URL", p);
}`)
	runScript(t, compile(t, "test.ts", src))

	want := []seen{
		{"POST", "1", "yes"}, {"PUT", "2", "yes"}, {"PATCH", "3", "yes"},
		{"DELETE", "", "yes"}, {"OPTIONS", "5", "yes"}, {"GET", "", "yes"},
	}
	if len(got) != len(want) {
		t.Fatalf("server saw %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRequestBodyMustBeAString(t *testing.T) {
	src, _ := server(t, ok, `import http from "loadtool/http";
export default function () { http.post("BASE_URL", { a: 1 }); }`)
	s := iterate(newVU(t, compile(t, "test.ts", src)))
	if s.ScriptErrors != 1 || !strings.Contains(s.FirstScriptError, "use JSON.stringify") {
		t.Fatalf("got %d errors, first %q; want a TypeError suggesting JSON.stringify", s.ScriptErrors, s.FirstScriptError)
	}
	if s.Requests != 0 {
		t.Errorf("Requests = %d, want 0: the request must not be sent", s.Requests)
	}
}

func TestDiscardResponseBodies(t *testing.T) {
	src, _ := server(t, ok, `import http from "loadtool/http";
export default function () {
	const res = http.get("BASE_URL");
	if (res.status !== 200) throw new Error("status " + res.status);
	if (res.body !== null) throw new Error("body " + res.body);
	try {
		res.json();
	} catch (e) {
		if (String(e).includes("discardResponseBodies")) return;
		throw e;
	}
	throw new Error("json() did not throw");
}`)
	runScript(t, compile(t, "test.ts", src).WithDiscardResponseBodies(true))
}

func TestResponseJSONErrors(t *testing.T) {
	src, _ := server(t, ok, `import http from "loadtool/http";
export default function () {
	let caught = "";
	try { http.get("BASE_URL").json(); } catch (e) { caught = e instanceof SyntaxError ? "SyntaxError" : String(e); }
	if (caught !== "SyntaxError") throw new Error("invalid JSON: caught " + caught);

	const failed = http.get("http://127.0.0.1:1/");
	if (failed.status !== 0 || failed.error === "" || failed.body !== null) {
		throw new Error("transport failure: " + JSON.stringify(failed));
	}
	try { failed.json(); } catch (e) {
		if (String(e).includes("no response was received")) return;
		throw e;
	}
	throw new Error("json() on a failed request did not throw");
}`)
	runScript(t, compile(t, "test.ts", src))
}

func TestUnsupportedParamsWarnOncePerRun(t *testing.T) {
	src, _ := server(t, ok, `import http from "loadtool/http";
export default function () {
	http.get("BASE_URL", { timeout: "5s", tags: { a: "b" } });
	http.get("BASE_URL", { timeout: "5s" });
}`)
	var warnings []string
	var mu sync.Mutex
	p := compile(t, "test.ts", src).WithWarn(func(msg string) {
		mu.Lock()
		warnings = append(warnings, msg)
		mu.Unlock()
	})
	for range 3 { // several VUs share one warner
		runScript(t, p)
	}
	want := []string{
		`http params key "timeout" is not supported yet and was ignored`,
		`http params key "tags" is not supported yet and was ignored`,
	}
	if strings.Join(warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}

// Named imports a script does not use are dropped from the bundle, so
// their methods are never built in any VU.
func TestUnusedHTTPExportsAreDropped(t *testing.T) {
	code, err := transpile("test.ts", "", []byte(`import { get } from "loadtool/http";
export default function () { get("http://x"); }`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "m.get") {
		t.Fatalf("bundle does not read m.get:\n%s", code)
	}
	for _, name := range []string{"post", "put", "patch", "del", "request"} {
		if strings.Contains(code, "m."+name) {
			t.Errorf("bundle reads unused export m.%s:\n%s", name, code)
		}
	}
}
