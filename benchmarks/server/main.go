// Command server is the shared, deterministic target for LoadTool, k6 and
// JMeter benchmark runs. It uses only the Go standard library and nothing
// from LoadTool, so every tool is measured against the same independent
// server.
//
//	go run ./benchmarks/server -addr 127.0.0.1:8080 -delay 10ms
//
// Endpoints:
//
//	GET /api/test  200, fixed JSON body, after the configured delay
//	GET /health    200, {"status":"ok"}, immediately
//
// Other paths return 404 and other methods 405.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"
)

// Response bodies are constants so every response is byte-for-byte
// identical: no timestamps, IDs or counters.
const (
	testBody   = `{"id":1,"name":"loadtool-benchmark","status":"ok","items":[1,2,3]}`
	healthBody = `{"status":"ok"}`
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	delay := flag.Duration("delay", 10*time.Millisecond, "fixed time /api/test waits before responding")
	flag.Parse()

	if *delay < 0 {
		log.Fatal("-delay must not be negative")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("benchmark server listening on http://%s (GET /api/test delay %s, GET /health)\n", ln.Addr(), *delay)
	if err := serve(ctx, ln, newMux(*delay)); err != nil {
		log.Fatal(err)
	}
}

// newMux routes the benchmark endpoints. "GET" patterns also answer HEAD,
// and the mux returns 405 for other methods and 404 for unknown paths.
func newMux(delay time.Duration) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /api/test", jsonHandler(testBody, delay))
	mux.Handle("GET /health", jsonHandler(healthBody, 0))
	return mux
}

// jsonHandler responds 200 with body after delay. A request cancelled by
// the client during the delay gets no response.
func jsonHandler(body string, delay time.Duration) http.Handler {
	b := []byte(body)
	length := strconv.Itoa(len(b))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			t := time.NewTimer(delay)
			select {
			case <-t.C:
			case <-r.Context().Done():
				t.Stop()
				return
			}
		}
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Content-Length", length)
		// Suppress the automatic Date header so responses are identical.
		h["Date"] = nil
		w.Write(b)
	})
}

// serve runs the HTTP server on ln until ctx is done, then shuts it down,
// giving in-flight requests up to 5 seconds to finish.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
