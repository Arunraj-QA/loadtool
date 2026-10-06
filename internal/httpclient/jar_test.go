package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

func TestJarIsLazy(t *testing.T) {
	var j Jar
	u, _ := url.Parse("http://example.test/")
	if got := j.Cookies(u); got != nil || j.cookies != nil {
		t.Fatalf("empty jar: Cookies = %v, inner jar allocated = %v", got, j.cookies != nil)
	}
	j.SetCookies(u, nil)
	if j.cookies != nil {
		t.Fatal("setting no cookies allocated the jar")
	}
	j.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "1"}})
	if got := j.Cookies(u); len(got) != 1 || got[0].Value != "1" {
		t.Fatalf("Cookies = %v, want sid=1", got)
	}
	j.Reset()
	if got := j.Cookies(u); got != nil {
		t.Errorf("after Reset: Cookies = %v, want none", got)
	}
}

// A per-VU client keeps its own cookies and shares the transport.
func TestWithJarKeepsSessionsApart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: r.URL.Query().Get("user"), Path: "/"})
			return
		}
		c, err := r.Cookie("sid")
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-User", c.Value)
	}))
	t.Cleanup(srv.Close)
	shared := New(2, DefaultTimeout)
	a, b := WithJar(shared, &Jar{}), WithJar(shared, &Jar{})
	if a.Transport != shared.Transport || b.Transport != shared.Transport {
		t.Fatal("per-VU clients must share the transport")
	}

	rec := &metrics.Recorder{}
	Do(context.Background(), a, get(srv.URL+"/login?user=alice"), rec)
	if res := Do(context.Background(), a, get(srv.URL+"/me"), rec); res.Header.Get("X-User") != "alice" {
		t.Errorf("a: status %d user %q, want alice's session", res.Status, res.Header.Get("X-User"))
	}
	if res := Do(context.Background(), b, get(srv.URL+"/me"), rec); res.Status != http.StatusUnauthorized {
		t.Errorf("b: status %d, want 401: b must not see a's cookie", res.Status)
	}
}

func TestNoConnectionReuse(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client := NewWithOptions(Options{MaxConnsPerHost: 1, Timeout: DefaultTimeout, NoConnectionReuse: true})
	rec := &metrics.Recorder{}
	for range 5 {
		Do(context.Background(), client, get(srv.URL), rec)
	}
	if n := conns.Load(); n != 5 {
		t.Errorf("opened %d connections for 5 requests, want 5 (one each)", n)
	}
}

// Without cookies the jar adds nothing to a request's allocations.
func BenchmarkDoWithEmptyJar(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	shared := New(1, DefaultTimeout)
	defer shared.CloseIdleConnections()
	client := WithJar(shared, &Jar{})
	rec := &metrics.Recorder{}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		Do(ctx, client, get(srv.URL), rec)
	}
}
