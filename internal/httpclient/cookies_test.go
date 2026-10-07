package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// cookieSite is a test server that plays several sites: a client from
// client() resolves every host name to it. /set?c=<Set-Cookie value> sets
// a cookie; every response's X-Got header is the Cookie header received.
type cookieSite struct {
	*httptest.Server
	port string
}

func newCookieSite(t *testing.T) *cookieSite {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.URL.Query().Get("c"); v != "" {
			w.Header().Add("Set-Cookie", v)
		}
		w.Header().Set("X-Got", r.Header.Get("Cookie"))
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return &cookieSite{Server: srv, port: port}
}

// client returns a client with its own jar whose connections all go to
// the test server, whatever the host name.
func (s *cookieSite) client() *http.Client {
	shared := New(4, 5*time.Second)
	tr := shared.Transport.(*http.Transport)
	addr := s.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	return WithJar(shared, &Jar{})
}

// get requests http://host:port/path and returns the Cookie header the
// server received.
func (s *cookieSite) get(t *testing.T, c *http.Client, host, path string) string {
	t.Helper()
	res := Do(context.Background(), c, get("http://"+host+":"+s.port+path), &metrics.Recorder{})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	return res.Header.Get("X-Got")
}

func set(cookie string) string {
	return "/set?c=" + strings.ReplaceAll(strings.ReplaceAll(cookie, ";", "%3B"), " ", "%20")
}

// The jar applies normal cookie rules (RFC 6265, through net/http/cookiejar).
func TestCookieSemantics(t *testing.T) {
	tests := []struct {
		name  string
		setOn string // host that sets the cookie
		set   string // Set-Cookie value
		host  string // host of the next request
		path  string // path of the next request
		want  string // Cookie header sent
	}{
		{"same host", "app.test", "sid=1", "app.test", "/", "sid=1"},
		{"host-only cookie not sent to a subdomain", "app.test", "sid=1", "api.app.test", "/", ""},
		{"host-only cookie not sent to another host", "app.test", "sid=1", "other.test", "/", ""},
		{"domain cookie sent to subdomains", "app.test", "sid=1; Domain=app.test", "api.app.test", "/", "sid=1"},
		{"path matches below it", "app.test", "sid=1; Path=/admin", "app.test", "/admin/users", "sid=1"},
		{"path does not match elsewhere", "app.test", "sid=1; Path=/admin", "app.test", "/shop", ""},
		{"Secure cookie not sent over http", "app.test", "sid=1; Secure", "app.test", "/", ""},
		{"Max-Age=0 is not stored", "app.test", "sid=1; Max-Age=0", "app.test", "/", ""},
		{"expired cookie is not stored", "app.test", "sid=1; Expires=Thu, 01 Jan 2015 00:00:00 GMT", "app.test", "/", ""},
		{"future expiry is kept", "app.test", "sid=1; Max-Age=3600", "app.test", "/", "sid=1"},
	}
	site := newCookieSite(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := site.client()
			site.get(t, c, tt.setOn, set(tt.set))
			if got := site.get(t, c, tt.host, tt.path); got != tt.want {
				t.Errorf("Cookie sent = %q, want %q", got, tt.want)
			}
		})
	}
}

// A later Set-Cookie replaces a cookie, and Max-Age=0 deletes it: the
// usual logout.
func TestCookieReplaceAndDelete(t *testing.T) {
	site := newCookieSite(t)
	c := site.client()
	site.get(t, c, "app.test", set("sid=first; Path=/"))
	site.get(t, c, "app.test", set("sid=second; Path=/"))
	if got := site.get(t, c, "app.test", "/"); got != "sid=second" {
		t.Fatalf("after replacing: %q, want sid=second", got)
	}
	site.get(t, c, "app.test", set("sid=x; Path=/; Max-Age=0"))
	if got := site.get(t, c, "app.test", "/"); got != "" {
		t.Errorf("after deleting: %q, want no cookie", got)
	}
}
