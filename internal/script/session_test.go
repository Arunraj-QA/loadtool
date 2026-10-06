package script

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
)

// sessionServer: /login?user=x sets sid=x; /me answers 200 with the user
// in X-User when the sid cookie is present, 401 otherwise; /echo returns
// the Cookie header it received.
func sessionServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: r.URL.Query().Get("user"), Path: "/", HttpOnly: true, MaxAge: 60})
		case "/me":
			c, err := r.Cookie("sid")
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("X-User", c.Value)
		case "/echo":
			w.Write([]byte(r.Header.Get("Cookie")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// sessionVU compiles src (BASE_URL replaced) into a VU with a real
// shared client, as the runner sets it up.
func sessionVU(t *testing.T, p *Program) *VU {
	t.Helper()
	vu, err := p.NewVU(context.Background(), 1, httpclient.New(2, httpclient.DefaultTimeout))
	if err != nil {
		t.Fatal(err)
	}
	return vu
}

const sessionScript = `import http from "loadtool/http";
export default function () {
	const before = http.get("BASE_URL/me").status;
	http.get("BASE_URL/login?user=u" + __VU);
	const after = http.get("BASE_URL/me");
	if (after.status !== 200 || after.headers["X-User"] !== "u" + __VU) throw new Error("login did not stick: " + after.status);
	(globalThis as any).before = before;
}`

func TestCookiesKeepASessionWithinAnIteration(t *testing.T) {
	url := sessionServer(t)
	vu := sessionVU(t, compile(t, "test.ts", strings.ReplaceAll(sessionScript, "BASE_URL", url)))
	for i := range 2 {
		if s := iterate(vu); s.ScriptErrors != 0 {
			t.Fatal(s.FirstScriptError)
		}
		// Every iteration starts logged out: the jar is reset.
		if got := vu.rt.Get("before").ToInteger(); got != 401 {
			t.Errorf("iteration %d started with status %d, want 401 (new session)", i, got)
		}
	}
}

func TestNoCookiesResetKeepsTheSession(t *testing.T) {
	url := sessionServer(t)
	vu := sessionVU(t, compile(t, "test.ts", strings.ReplaceAll(sessionScript, "BASE_URL", url)).WithKeepCookies(true))
	iterate(vu)
	if s := iterate(vu); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	if got := vu.rt.Get("before").ToInteger(); got != 200 {
		t.Errorf("second iteration started with status %d, want 200 (session kept)", got)
	}
}

func TestVUsHaveSeparateSessions(t *testing.T) {
	url := sessionServer(t)
	p := compile(t, "test.ts", strings.ReplaceAll(`import http from "loadtool/http";
export default function () {
	if (__VU === 1) http.get("BASE_URL/login?user=one");
	(globalThis as any).status = http.get("BASE_URL/me").status;
}`, "BASE_URL", url)).WithKeepCookies(true)
	shared := httpclient.New(2, httpclient.DefaultTimeout)
	a, err := p.NewVU(context.Background(), 1, shared)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewVU(context.Background(), 2, shared)
	if err != nil {
		t.Fatal(err)
	}
	iterate(a)
	iterate(b)
	if got := a.rt.Get("status").ToInteger(); got != 200 {
		t.Errorf("VU 1 status %d, want 200", got)
	}
	if got := b.rt.Get("status").ToInteger(); got != 401 {
		t.Errorf("VU 2 status %d, want 401: it must not see VU 1's cookie", got)
	}
}

func TestParamsCookiesAndResCookies(t *testing.T) {
	url := sessionServer(t)
	p := compile(t, "test.ts", strings.ReplaceAll(`import http from "loadtool/http";
export default function () {
	const login = http.get("BASE_URL/login?user=ann");
	const c = login.cookies.sid[0];
	if (c.name !== "sid" || c.value !== "ann" || c.path !== "/" || !c.http_only || c.secure || c.max_age !== 60) {
		throw new Error("res.cookies " + JSON.stringify(login.cookies));
	}
	if (Object.keys(http.get("BASE_URL/me").cookies).length !== 0) throw new Error("no cookies set, want {}");

	// params.cookies are sent in addition to the jar's.
	const echo = http.get("BASE_URL/echo", { cookies: { theme: "dark" } }).body;
	if (echo !== "theme=dark; sid=ann") throw new Error("Cookie header " + echo);
}`, "BASE_URL", url))
	if s := iterate(sessionVU(t, p)); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
}

// setup and teardown share one jar, which the VUs never see.
func TestLifecycleSessionIsSeparate(t *testing.T) {
	url := sessionServer(t)
	p := compile(t, "test.ts", strings.ReplaceAll(`import http from "loadtool/http";
export function setup() { http.get("BASE_URL/login?user=admin"); }
export default function () {
	if (http.get("BASE_URL/me").status !== 401) throw new Error("a VU saw setup's session");
}
export function teardown() {
	if (http.get("BASE_URL/me").headers["X-User"] !== "admin") throw new Error("teardown lost setup's session");
}`, "BASE_URL", url))
	shared := httpclient.New(2, httpclient.DefaultTimeout)
	l, err := p.NewLifecycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Setup(context.Background(), shared, time.Minute); err != nil {
		t.Fatal(err)
	}
	vu, err := p.NewVU(context.Background(), 1, shared)
	if err != nil {
		t.Fatal(err)
	}
	if s := iterate(vu); s.ScriptErrors != 0 {
		t.Fatal(s.FirstScriptError)
	}
	if err := l.Teardown(context.Background(), shared, time.Minute, nil); err != nil {
		t.Fatal(err)
	}
}
