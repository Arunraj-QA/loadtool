package httpclient

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// BenchmarkProtocols compares HTTP/1.1 and HTTP/2, over TLS and in
// cleartext, for one request at a time ("serial") and about 64 requests in
// flight ("parallel"), against a local server answering a small JSON
// body, like the benchmark server. It reports the connections the server
// accepted. Loopback hides network latency, so this measures the client's
// own cost per request, not what a real network would show.
func BenchmarkProtocols(b *testing.B) {
	body := []byte(`{"id":1,"name":"loadtool-benchmark","status":"ok","items":[1,2,3]}`)
	for _, p := range []struct {
		name    string
		tls     bool
		version string
	}{
		{"http1-cleartext", false, HTTP11},
		{"h2c", false, HTTP2},
		{"http1-tls", true, HTTP11},
		{"http2-tls", true, HTTP2},
	} {
		for _, mode := range []string{"serial", "parallel"} {
			b.Run(p.name+"/"+mode, func(b *testing.B) {
				var conns atomic.Int64
				srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Write(body)
				}))
				// Closing the server can interrupt a handshake; its log line
				// would break the benchmark output.
				srv.Config.ErrorLog = log.New(io.Discard, "", 0)
				srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
					if s == http.StateNew {
						conns.Add(1)
					}
				}
				o := Options{MaxConnsPerHost: 64, Timeout: DefaultTimeout, HTTPVersion: p.version}
				if p.tls {
					srv.EnableHTTP2 = true
					srv.StartTLS()
					o.TLSConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
				} else {
					srv.Config.Protocols = new(http.Protocols)
					srv.Config.Protocols.SetHTTP1(true)
					srv.Config.Protocols.SetUnencryptedHTTP2(true)
					srv.Start()
				}
				defer srv.Close()
				client := NewWithOptions(o)
				defer client.CloseIdleConnections()
				ctx := context.Background()
				req := Request{Method: http.MethodGet, URL: srv.URL, KeepBody: true}
				// Connect first, so handshakes are not timed.
				Do(ctx, client, req, &metrics.Recorder{})

				b.ReportAllocs()
				b.ResetTimer()
				if mode == "serial" {
					rec := &metrics.Recorder{}
					for range b.N {
						Do(ctx, client, req, rec)
					}
				} else {
					b.SetParallelism(max(1, 64/runtime.GOMAXPROCS(0)))
					b.RunParallel(func(pb *testing.PB) {
						rec := &metrics.Recorder{}
						for pb.Next() {
							Do(ctx, client, req, rec)
						}
					})
				}
				b.StopTimer()
				b.ReportMetric(float64(conns.Load()), "conns")
			})
		}
	}
}
