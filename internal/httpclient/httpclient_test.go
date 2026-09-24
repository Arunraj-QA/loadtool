package httpclient

import (
	"context"
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

func summarize(rec *metrics.Recorder) metrics.Summary {
	return metrics.Merge([]*metrics.Recorder{rec})
}

func TestGetOutcome(t *testing.T) {
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
			Get(context.Background(), New(1, DefaultTimeout), srv.URL, rec)

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

func TestGetRecordsLatency(t *testing.T) {
	const delay = 20 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
	}))
	t.Cleanup(srv.Close)

	rec := &metrics.Recorder{}
	Get(context.Background(), New(1, DefaultTimeout), srv.URL, rec)
	// Allow for coarse clocks: Windows' monotonic clock ticks every ~0.5ms.
	if got := summarize(rec).Max; got < delay-2*time.Millisecond {
		t.Fatalf("latency = %v, want at least ~%v", got, delay)
	}
}

func TestGetTransportErrorIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more

	rec := &metrics.Recorder{}
	Get(context.Background(), New(1, DefaultTimeout), url, rec)
	if s := summarize(rec); s.Requests != 1 || s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failed request", s)
	}
}

func TestGetTimeoutIsFailure(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	rec := &metrics.Recorder{}
	Get(context.Background(), New(1, 50*time.Millisecond), srv.URL, rec)
	if s := summarize(rec); s.Failures != 1 {
		t.Fatalf("got %+v, want 1 failed request", s)
	}
}

func TestGetCancelledIsNotRecorded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	rec := &metrics.Recorder{}
	Get(ctx, New(1, DefaultTimeout), srv.URL, rec)
	if s := summarize(rec); s.Requests != 0 {
		t.Fatalf("cancelled request was recorded: %+v", s)
	}
}

func TestGetReusesConnections(t *testing.T) {
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
		Get(context.Background(), client, srv.URL, rec)
	}
	if s := summarize(rec); s.Successes != 20 {
		t.Fatalf("got %+v, want 20 successes", s)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("opened %d connections for 20 sequential requests, want 1", n)
	}
}

func TestGetDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	rec := &metrics.Recorder{}
	Get(context.Background(), New(1, DefaultTimeout), srv.URL, rec)
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
	Get(context.Background(), client, srv.URL, rec)
	if s := summarize(rec); s.Successes != 1 {
		t.Fatalf("got %+v, want 1 success", s)
	}
	if got := proto.Load(); got != "HTTP/1.1" {
		t.Errorf("server saw %v, want HTTP/1.1", got)
	}
}

func BenchmarkGet(b *testing.B) {
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
		Get(ctx, client, srv.URL, rec)
	}
}
