package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte("body"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(url string) Request {
	return Request{Method: http.MethodGet, URL: url}
}

func summarize(rec *metrics.Recorder) metrics.Summary {
	return metrics.Merge([]*metrics.Recorder{rec})
}

func TestDoOutcome(t *testing.T) {
	tests := []struct {
		status int
		wantOK bool
	}{
		{http.StatusOK, true},
		{http.StatusNoContent, true},
		{http.StatusFound, true},
		{http.StatusNotFound, false},
		{http.StatusInternalServerError, false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := statusServer(t, tt.status)
			rec := &metrics.Recorder{}
			Do(context.Background(), New(1, DefaultTimeout), get(srv.URL), rec)

			s := summarize(rec)
			if s.Requests != 1 {
				t.Fatalf("Requests = %d, want 1", s.Requests)
			}
			if gotOK := s.Successes == 1; gotOK != tt.wantOK {
				t.Errorf("success = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

func TestDoRecordsLatency(t *testing.T) {
	const delay = 20 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
	}))
	t.Cleanup(srv.Close)

	rec := &metrics.Recorder{}
	Do(context.Background(), New(1, DefaultTimeout), get(srv.URL), rec)
	// Allow for coarse clocks: Windows' monotonic clock ticks every ~0.5ms.
	if got := summarize(rec).Max; got < delay-2*time.Millisecond {
		t.Fatalf("latency = %v, want at least ~%v", got, delay)
	}
}

func TestDoTransportErrorIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more

	rec := &metrics.Recorder{}
	Do(context.Background(), New(1, DefaultTimeout), get(url), rec)
	if s := summarize(rec); s.Requests != 1 || s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failed request", s)
	}
}

func TestDoTimeoutIsFailure(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	rec := &metrics.Recorder{}
	Do(context.Background(), New(1, 50*time.Millisecond), get(srv.URL), rec)
	if s := summarize(rec); s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failed request", s)
	}
}

func TestDoCancelledIsNotRecorded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	rec := &metrics.Recorder{}
	Do(ctx, New(1, DefaultTimeout), get(srv.URL), rec)
	if s := summarize(rec); s.Requests != 0 {
		t.Fatalf("cancelled request was recorded: %+v", s)
	}
}

func TestDoReusesConnections(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client := New(1, DefaultTimeout)
	rec := &metrics.Recorder{}
	for range 20 {
		Do(context.Background(), client, get(srv.URL), rec)
	}
	if s := summarize(rec); s.Successes != 20 {
		t.Fatalf("got %+v, want 20 successes", s)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("opened %d connections for 20 sequential requests, want 1", n)
	}
}

func TestDoDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	rec := &metrics.Recorder{}
	Do(context.Background(), New(1, DefaultTimeout), get(srv.URL), rec)
	if n := hits.Load(); n != 1 {
		t.Fatalf("server hit %d times, want 1 (redirect must not be followed)", n)
	}
}

func TestClientUsesHTTP1OverTLS(t *testing.T) {
	var proto atomic.Value
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto.Store(r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := New(1, DefaultTimeout)
	// Trust the test server's certificate while keeping our HTTP/1.1 settings.
	client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig

	rec := &metrics.Recorder{}
	Do(context.Background(), client, get(srv.URL), rec)
	if s := summarize(rec); s.Successes != 1 {
		t.Fatalf("got %+v, want 1 success", s)
	}
	if got := proto.Load(); got != "HTTP/1.1" {
		t.Errorf("server saw %v, want HTTP/1.1", got)
	}
}

func BenchmarkDo(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	client := New(1, DefaultTimeout)
	defer client.CloseIdleConnections()
	rec := &metrics.Recorder{}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		Do(ctx, client, get(srv.URL), rec)
	}
}

func TestDoSendsMethodBodyAndHeaders(t *testing.T) {
	type seen struct{ method, body, header string }
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, string(b), r.Header.Get("X-Test")}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	rec := &metrics.Recorder{}
	res := Do(context.Background(), New(1, DefaultTimeout), Request{
		Method: http.MethodPost,
		URL:    srv.URL,
		Body:   `{"a":1}`,
		Header: http.Header{"X-Test": {"yes"}},
	}, rec)

	if res.Status != http.StatusCreated || !res.OK() || res.Err != nil {
		t.Fatalf("result = %+v, want 201 OK", res)
	}
	want := seen{http.MethodPost, `{"a":1}`, "yes"}
	if s := <-got; s != want {
		t.Errorf("server saw %+v, want %+v", s, want)
	}
}

func TestDoInvalidRequest(t *testing.T) {
	rec := &metrics.Recorder{}
	res := Do(context.Background(), New(1, DefaultTimeout), Request{Method: "BAD METHOD", URL: "http://x"}, rec)
	if res.Err == nil || res.OK() {
		t.Fatalf("result = %+v, want error", res)
	}
	if s := summarize(rec); s.Failures != 1 || s.Sent != 0 || s.Max != 0 {
		t.Fatalf("got %+v, want 1 failure and no latency sample (nothing was sent)", s)
	}
}

func TestResultOK(t *testing.T) {
	tests := []struct {
		res  Result
		want bool
	}{
		{Result{Status: 200}, true},
		{Result{Status: 399}, true},
		{Result{Status: 400}, false},
		{Result{Status: 0}, false},
		{Result{Status: 200, Err: io.ErrUnexpectedEOF}, false},
	}
	for _, tt := range tests {
		if got := tt.res.OK(); got != tt.want {
			t.Errorf("%+v.OK() = %v, want %v", tt.res, got, tt.want)
		}
	}
}

// TestNewCapsConnectionsPerHost guards against unbounded background dials:
// requests beyond the cap must wait for a connection instead of opening more.
func TestNewCapsConnectionsPerHost(t *testing.T) {
	const limit, requests = 2, 10
	var open, maxOpen atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			n := open.Add(1)
			for {
				m := maxOpen.Load()
				if n <= m || maxOpen.CompareAndSwap(m, n) {
					break
				}
			}
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client := New(limit, DefaultTimeout)
	t.Cleanup(client.CloseIdleConnections)
	rec := make([]*metrics.Recorder, requests)
	var wg sync.WaitGroup
	for i := range requests {
		rec[i] = &metrics.Recorder{}
		wg.Go(func() { Do(context.Background(), client, get(srv.URL), rec[i]) })
	}
	// Let every request reach the transport before any response is sent.
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()

	if s := metrics.Merge(rec); s.Successes != requests {
		t.Fatalf("got %+v, want %d successes", s, requests)
	}
	if m := maxOpen.Load(); m > limit {
		t.Errorf("opened %d connections at once, want at most %d", m, limit)
	}
}

// TestDoSendsNoImplicitAcceptEncoding keeps LoadTool's requests equivalent
// to k6 and JMeter, which do not ask for compression unless told to.
func TestDoSendsNoImplicitAcceptEncoding(t *testing.T) {
	got := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Accept-Encoding")
	}))
	t.Cleanup(srv.Close)
	client := New(1, DefaultTimeout)
	rec := &metrics.Recorder{}

	Do(context.Background(), client, get(srv.URL), rec)
	if ae := <-got; ae != "" {
		t.Errorf("default request sent Accept-Encoding %q, want none", ae)
	}

	Do(context.Background(), client, Request{
		Method: http.MethodGet, URL: srv.URL, Header: http.Header{"Accept-Encoding": {"br"}},
	}, rec)
	if ae := <-got; ae != "br" {
		t.Errorf("explicit Accept-Encoding = %q, want %q", ae, "br")
	}
}

func TestDoKeepsBodyAndHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reply", "yes")
		switch r.URL.Path {
		case "/sized":
			w.Header().Set("Content-Length", "5")
			w.Write([]byte("sized"))
		case "/chunked":
			// Flushing before the end forces chunked encoding: no
			// Content-Length, so the body is read until EOF.
			w.Write([]byte("chun"))
			w.(http.Flusher).Flush()
			w.Write([]byte("ked"))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	client := New(1, DefaultTimeout)

	for _, tt := range []struct{ path, want string }{
		{"/sized", "sized"}, {"/chunked", "chunked"}, {"/empty", ""},
	} {
		t.Run(tt.path, func(t *testing.T) {
			res := Do(context.Background(), client,
				Request{Method: http.MethodGet, URL: srv.URL + tt.path, KeepBody: true}, &metrics.Recorder{})
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			if res.Body == nil || string(res.Body) != tt.want {
				t.Errorf("Body = %q (nil: %v), want %q", res.Body, res.Body == nil, tt.want)
			}
			if got := res.Header.Get("X-Reply"); got != "yes" {
				t.Errorf("Header X-Reply = %q, want yes", got)
			}
		})
	}
}

func TestDoDiscardsBodyByDefault(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	res := Do(context.Background(), New(1, DefaultTimeout), get(srv.URL), &metrics.Recorder{})
	if res.Body != nil {
		t.Errorf("Body = %q, want nil when KeepBody is not set", res.Body)
	}
}

func BenchmarkDoKeepBody(b *testing.B) {
	payload := []byte(`{"id":1,"name":"product","price":9.99,"tags":["a","b","c"]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()
	client := New(1, DefaultTimeout)
	defer client.CloseIdleConnections()
	rec := &metrics.Recorder{}
	ctx := context.Background()
	req := Request{Method: http.MethodGet, URL: srv.URL, KeepBody: true}

	b.ReportAllocs()
	for b.Loop() {
		Do(ctx, client, req, rec)
	}
}
