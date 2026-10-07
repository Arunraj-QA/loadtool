package runner

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/config"
)

// sessionApp is a small app with cookie sessions:
//
//	POST /login?user=u   sets sid=<token for u>
//	GET  /me             200 with X-User when logged in, else 401
//	POST /logout         deletes the cookie (Max-Age=0)
//
// It records requests whose session belongs to a user other than the one
// the VU says it is (X-Expect), and the connections it accepted.
type sessionApp struct {
	*httptest.Server
	mu         sync.Mutex
	tokens     map[string]string // token -> user
	next       int
	crossed    atomic.Int64 // requests with someone else's session
	authorized atomic.Int64
	conns      atomic.Int64
}

func newSessionApp(t *testing.T) *sessionApp {
	t.Helper()
	a := &sessionApp{tokens: make(map[string]string)}
	a.Server = httptest.NewUnstartedServer(http.HandlerFunc(a.serve))
	a.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			a.conns.Add(1)
		}
	}
	a.Start()
	t.Cleanup(a.Close)
	return a
}

func (a *sessionApp) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/login":
		a.mu.Lock()
		a.next++
		token := fmt.Sprintf("t%d", a.next) // tokens never repeat
		a.tokens[token] = r.URL.Query().Get("user")
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: token, Path: "/", HttpOnly: true})
	case "/logout":
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", Path: "/", MaxAge: -1})
	case "/me":
		c, err := r.Cookie("sid")
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		a.mu.Lock()
		user := a.tokens[c.Value]
		a.mu.Unlock()
		if expect := r.Header.Get("X-Expect"); expect != "" && user != expect {
			a.crossed.Add(1)
		}
		a.authorized.Add(1)
		w.Header().Set("X-User", user)
	}
}

// A full login flow in every iteration: log in, use the session, log out,
// and be refused afterwards. Checks and thresholds see it all.
func TestRunLoginFlow(t *testing.T) {
	app := newSessionApp(t)
	res, err := runScript(t, `import http from "loadtool/http";
import { check } from "loadtool";
export const options = {
	vus: 5, duration: "300ms",
	thresholds: { checks: ["rate==1"], http_req_failed: ["rate<0.5"] },
};
const BASE = "`+app.URL+`";
export default function () {
	const before = http.get(BASE + "/me");
	const login = http.post(BASE + "/login?user=u" + __VU, "");
	const me = http.get(BASE + "/me");
	http.post(BASE + "/logout", "");
	const after = http.get(BASE + "/me");
	check(null, {
		"logged out at the start of the iteration": () => before.status === 401,
		"login set a cookie": () => login.cookies.sid !== undefined,
		"session recognised": () => me.status === 200 && me.headers["X-User"] === "u" + __VU,
		"logged out after logout": () => after.status === 401,
	});
}`, config.Overrides{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.ScriptErrors != 0 || len(s.Checks) != 4 {
		t.Fatalf("summary %+v", s)
	}
	for _, c := range s.Checks {
		if c.Fails != 0 || c.Passes == 0 {
			t.Errorf("check %q: %d passed, %d failed", c.Name, c.Passes, c.Fails)
		}
	}
	for _, th := range res.Thresholds {
		if !th.Passed {
			t.Errorf("threshold %s %s failed", th.Metric, th.Expr)
		}
	}
}

// Many VUs logged in at the same time never see each other's sessions,
// with the jar reset every iteration or kept for the whole test (run with
// -race).
func TestRunSessionsAreIsolatedAcrossConcurrentVUs(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("noCookiesReset=%v", keep), func(t *testing.T) {
			app := newSessionApp(t)
			// With the jar kept, each VU logs in only in its first
			// iteration and must stay itself for the rest of the test.
			login := `http.post(BASE + "/login?user=u" + __VU, "");`
			if keep {
				login = `if (__ITER === 0) { ` + login + ` }`
			} else {
				// With the default reset, every iteration starts logged out
				// (no logout here, so only the reset can do that).
				login = `if (http.get(BASE + "/me").status !== 401) throw new Error("iteration started logged in");
	` + login
			}
			res, err := runScript(t, `import http from "loadtool/http";
export const options = { vus: 50, duration: "500ms", noCookiesReset: `+fmt.Sprint(keep)+` };
const BASE = "`+app.URL+`";
export default function () {
	`+login+`
	for (let i = 0; i < 3; i++) {
		const me = http.get(BASE + "/me", { headers: { "X-Expect": "u" + __VU } });
		if (me.status !== 200) throw new Error("not logged in: " + me.status);
		if (me.headers["X-User"] !== "u" + __VU) throw new Error("session of " + me.headers["X-User"]);
	}
}`, config.Overrides{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if s := res.Summary; s.ScriptErrors != 0 {
				t.Fatalf("%d script errors, first: %s", s.ScriptErrors, s.FirstScriptError)
			}
			if n := app.crossed.Load(); n != 0 {
				t.Errorf("%d requests carried another VU's session", n)
			}
			if n := app.authorized.Load(); n < 50*3 {
				t.Errorf("only %d authorized requests; want at least one iteration per VU", n)
			}
			// Cookies do not get in the way of connection reuse: at most
			// one connection per VU.
			if n := app.conns.Load(); n > 50 {
				t.Errorf("opened %d connections for 50 VUs, want at most 50", n)
			}
		})
	}
}
