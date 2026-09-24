// Package httpclient executes HTTP/1.1 requests for load tests and records
// their latency and outcome.
package httpclient

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
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

// Request describes one HTTP request.
type Request struct {
	Method string
	URL    string
	// Body is sent as-is; empty means no body.
	Body string
	// Header may be nil.
	Header http.Header
}

// Result is the outcome of one request.
type Result struct {
	// Status is the HTTP status code, or 0 if no response was received.
	Status int
	// Duration covers sending the request and reading the whole body.
	Duration time.Duration
	Err      error
}

// OK reports whether the request completed with a 2xx or 3xx status.
func (r Result) OK() bool {
	return r.Err == nil && r.Status >= 200 && r.Status < 400
}

// Do sends one request and records it in rec.
//
// Requests interrupted because ctx was cancelled (end of test or user
// interrupt) are not recorded, so stopping a test does not produce errors.
func Do(ctx context.Context, client *http.Client, r Request, rec *metrics.Recorder) Result {
	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		rec.Record(0, false)
		return Result{Err: err}
	}
	if r.Header != nil {
		req.Header = r.Header
	}

	var res Result
	start := time.Now()
	resp, err := client.Do(req)
	if err == nil {
		res.Status = resp.StatusCode
		// Drain the body so the connection returns to the pool.
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	res.Duration = time.Since(start)
	res.Err = err

	if ctx.Err() == nil {
		rec.Record(res.Duration, res.OK())
	}
	return res
}
