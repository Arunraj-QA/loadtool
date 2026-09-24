package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	if s := summarize(rec); s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failure", s)
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
