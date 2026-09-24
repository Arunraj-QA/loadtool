// Package httpclient executes HTTP/1.1 requests for load tests and records
// their latency and outcome.
package httpclient

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// DefaultTimeout bounds a single request, including reading the body.
const DefaultTimeout = 30 * time.Second

// New returns an HTTP/1.1 client tuned for load testing. It keeps up to
// maxConnsPerHost idle connections per host, so each VU can reuse its own
// keep-alive connection instead of dialing per request.
//
// Redirects are not followed, so each request is measured on its own.
func New(maxConnsPerHost int, timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = maxConnsPerHost
	t.MaxIdleConnsPerHost = maxConnsPerHost
	// Phase 0 is HTTP/1.1 only: disable HTTP/2 negotiation over TLS.
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}

	return &http.Client{
		Transport: t,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Get sends one GET request to url and records it in rec.
//
// A request is successful when it completes with a 2xx or 3xx status.
// Requests interrupted because ctx was cancelled (end of test or user
// interrupt) are not recorded, so stopping a test does not produce errors.
func Get(ctx context.Context, client *http.Client, url string, rec *metrics.Recorder) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		rec.Record(0, false)
		return
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err == nil {
		// Drain the body so the connection returns to the pool.
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	latency := time.Since(start)

	if ctx.Err() != nil {
		return
	}
	rec.Record(latency, err == nil && resp.StatusCode < 400)
}
