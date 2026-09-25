// Command server is the shared target for LoadTool, k6 and JMeter benchmark
// runs. Every request gets the same fixed-size body after the same fixed
// delay, so all tools are measured against identical server behaviour.
//
//	go run ./benchmarks/server -addr 127.0.0.1:8080 -delay 10ms -body-size 2
package main

import (
	"bytes"
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

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	delay := flag.Duration("delay", 10*time.Millisecond, "time to wait before responding")
	bodySize := flag.Int("body-size", 2, "response body size in bytes")
	flag.Parse()

	if *delay < 0 || *bodySize < 0 {
		log.Fatal("-delay and -body-size must not be negative")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("benchmark server listening on http://%s (delay %s, body %d bytes)\n", ln.Addr(), *delay, *bodySize)
	if err := serve(ctx, ln, handler(*delay, *bodySize)); err != nil {
		log.Fatal(err)
	}
}

// handler responds 200 with bodySize bytes after delay. A request cancelled
// by the client during the delay gets no response.
func handler(delay time.Duration, bodySize int) http.Handler {
	body := bytes.Repeat([]byte("x"), bodySize)
	length := strconv.Itoa(len(body))
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
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", length)
		w.Write(body)
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
