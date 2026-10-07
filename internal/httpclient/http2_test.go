package httpclient

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// h2Server is a TLS test server with HTTP/2 that counts the connections
// it accepts and the requests (streams) in progress.
type h2Server struct {
	*httptest.Server
	conns  atomic.Int64
	active atomic.Int64
}

func newH2Server(t *testing.T, h http.HandlerFunc, configure func(*http.Server)) *h2Server {
	t.Helper()
	s := &h2Server{}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.active.Add(1)
		defer s.active.Add(-1)
		h(w, r)
	}))
	// Tests that reject the certificate or close mid-handshake make the
	// server log; the tests check the outcome themselves.
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			s.conns.Add(1)
		}
	}
	if configure != nil {
		configure(s.Config)
	}
	s.EnableHTTP2 = true
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// client returns an HTTP/2-only client that trusts the server.
func (s *h2Server) client(o Options) *http.Client {
	o.HTTPVersion = HTTP2
	o.TLSConfig = s.Server.Client().Transport.(*http.Transport).TLSClientConfig
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.MaxConnsPerHost == 0 {
		o.MaxConnsPerHost = 10
	}
	return NewWithOptions(o)
}

// waitIdle waits until the server has no request in progress: every
// stream was finished or reset.
func (s *h2Server) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d requests still in progress on the server", s.active.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A request that takes longer than the client timeout fails like it does
// over HTTP/1.1: status 0, an error, counted as a failed request with its
// latency. The connection survives for the next request.
func TestHTTP2Timeout(t *testing.T) {
	srv := newH2Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
			}
		}
	}, nil)
	client := srv.client(Options{Timeout: 200 * time.Millisecond})
	rec := &metrics.Recorder{}

	start := time.Now()
	res := Do(context.Background(), client, get(srv.URL+"/slow"), rec)
	if res.Err == nil || res.Status != 0 || !strings.Contains(res.Err.Error(), "Client.Timeout") {
		t.Fatalf("result = %+v, want a client timeout", res)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("timed out after %v, want about 200ms", took)
	}
	if s := metrics.Merge([]*metrics.Recorder{rec}); s.Failures != 1 || s.Sent != 1 {
		t.Errorf("summary %+v, want one failed request with a latency", s)
	}

	srv.waitIdle(t) // the server saw the stream reset
	if res := Do(context.Background(), client, get(srv.URL+"/fast"), rec); res.Err != nil || res.Proto != "HTTP/2.0" {
		t.Fatalf("next request = %+v, want success over HTTP/2", res)
	}
	if n := srv.conns.Load(); n != 1 {
		t.Errorf("opened %d connections, want 1: a timed-out stream must not close the connection", n)
	}
}

// Cancelling a request (end of test, Ctrl+C) resets its stream: the
// server's handler sees the cancellation, the request is not counted,
// and the connection is reused.
func TestHTTP2CancellationResetsStream(t *testing.T) {
	started := make(chan struct{})
	serverSawCancel := make(chan struct{})
	srv := newH2Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/block" {
			return
		}
		close(started)
		select {
		case <-r.Context().Done():
			close(serverSawCancel)
		case <-time.After(10 * time.Second):
		}
	}, nil)
	client := srv.client(Options{})
	rec := &metrics.Recorder{}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	res := Do(ctx, client, get(srv.URL+"/block"), rec)
	if res.Err == nil {
		t.Fatal("want an error from the cancelled request")
	}
	select {
	case <-serverSawCancel:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not see the stream reset")
	}
	if s := metrics.Merge([]*metrics.Recorder{rec}); s.Requests != 0 {
		t.Errorf("cancelled request was counted: %+v", s)
	}
	if res := Do(context.Background(), client, get(srv.URL+"/next"), rec); res.Err != nil {
		t.Fatal(res.Err)
	}
	if n := srv.conns.Load(); n != 1 {
		t.Errorf("opened %d connections, want 1: a reset stream must not close the connection", n)
	}
}

// A server's concurrent-stream limit (RFC 9113 recommends at least 100)
// is handled by opening more connections: with more requests in flight
// than one connection allows, every request still succeeds.
//
// Go dials a new connection for each request waiting for a stream, so a
// large overflow means many simultaneous dials; on a slow machine a burst
// of hundreds overflows the server's accept queue and some are refused
// (benchmarks/results/2026-10-07-http2). The overflow here is kept to 50.
func TestHTTP2ConcurrentStreamLimit(t *testing.T) {
	srv := newH2Server(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond) // keep streams open together
	}, func(s *http.Server) {
		s.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 100}
	})
	client := srv.client(Options{MaxConnsPerHost: 150})
	// Establish the first connection, and learn the limit, before the burst.
	Do(context.Background(), client, get(srv.URL), &metrics.Recorder{})

	const requests = 150
	recs := metrics.NewRecorders(requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() { Do(context.Background(), client, get(srv.URL), recs[i]) })
	}
	wg.Wait()
	s := metrics.Merge(recs)
	if s.Successes != requests || s.Protocols.HTTP2 != requests {
		t.Fatalf("%d of %d requests succeeded over HTTP/2: %+v", s.Protocols.HTTP2, requests, s)
	}
	if n := srv.conns.Load(); n < 2 {
		t.Errorf("opened %d connection for %d concurrent requests, want more than one (100 streams each)", n, requests)
	}
}

// Against a server that allows fewer than 100 streams, Go's client may
// open streams on a new connection before the server's limit arrives
// (it assumes 100 until then), and the server refuses them. Such requests
// fail and are counted; none hangs or goes missing.
func TestHTTP2LowStreamLimitIsCounted(t *testing.T) {
	srv := newH2Server(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
	}, func(s *http.Server) {
		s.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 2}
	})
	client := srv.client(Options{MaxConnsPerHost: 20})
	const requests = 20
	recs := metrics.NewRecorders(requests)
	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := range requests {
		wg.Go(func() { Do(context.Background(), client, get(srv.URL), recs[i]) })
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("requests did not complete")
	}
	if s := metrics.Merge(recs); s.Requests != requests || s.Successes+s.Failures != requests {
		t.Errorf("summary %+v, want all %d requests counted", s, requests)
	}
}

// Large bodies, kept or discarded, are read to the end, so every stream
// finishes and one connection serves every request.
func TestHTTP2BodyCleanup(t *testing.T) {
	body := strings.Repeat("x", 256<<10) // larger than HTTP/2's initial flow-control window
	srv := newH2Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}, nil)
	client := srv.client(Options{})
	rec := &metrics.Recorder{}
	for i := range 40 {
		req := get(srv.URL)
		req.KeepBody = i%2 == 0
		res := Do(context.Background(), client, req, rec)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if req.KeepBody && len(res.Body) != len(body) {
			t.Fatalf("kept %d bytes, want %d", len(res.Body), len(body))
		}
	}
	srv.waitIdle(t)
	if n := srv.conns.Load(); n != 1 {
		t.Errorf("opened %d connections for 40 sequential requests, want 1", n)
	}
}

// HTTP/2 does not weaken TLS: an untrusted certificate fails the request.
func TestHTTP2VerifiesCertificates(t *testing.T) {
	srv := newH2Server(t, func(http.ResponseWriter, *http.Request) {}, nil)
	client := NewWithOptions(Options{MaxConnsPerHost: 1, Timeout: 5 * time.Second, HTTPVersion: HTTP2})
	res := Do(context.Background(), client, get(srv.URL), &metrics.Recorder{})
	if res.Err == nil || res.Status != 0 || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("result = %+v, want a certificate error", res)
	}
}

// noConnectionReuse applies to HTTP/2: a new connection per request.
func TestHTTP2NoConnectionReuse(t *testing.T) {
	srv := newH2Server(t, func(http.ResponseWriter, *http.Request) {}, nil)
	client := srv.client(Options{NoConnectionReuse: true})
	for range 5 {
		if res := Do(context.Background(), client, get(srv.URL), &metrics.Recorder{}); res.Err != nil || res.Proto != "HTTP/2.0" {
			t.Fatalf("result = %+v", res)
		}
	}
	if n := srv.conns.Load(); n != 5 {
		t.Errorf("opened %d connections for 5 requests, want 5", n)
	}
}

// Many goroutines share one HTTP/2 client and connection (run with
// -race): every request succeeds and is counted.
func TestHTTP2ParallelRequests(t *testing.T) {
	srv := newH2Server(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }, nil)
	client := srv.client(Options{MaxConnsPerHost: 64})
	const vus, perVU = 32, 50
	recs := metrics.NewRecorders(vus)
	var wg sync.WaitGroup
	for i := range vus {
		wg.Go(func() {
			for j := range perVU {
				req := get(srv.URL)
				req.KeepBody = j%2 == 0
				Do(context.Background(), client, req, recs[i])
			}
		})
	}
	wg.Wait()
	s := metrics.Merge(recs)
	if s.Successes != vus*perVU || s.Protocols.HTTP2 != vus*perVU {
		t.Fatalf("summary %+v, want %d successful HTTP/2 requests", s, vus*perVU)
	}
	srv.waitIdle(t)
}
