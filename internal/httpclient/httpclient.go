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

// New returns an HTTP/1.1 client tuned for load testing. Pass the VU count
// as maxConnsPerHost: each VU needs at most one connection per host, and
// keeps it alive between requests.
//
// maxConnsPerHost caps open plus dialing connections, not just idle ones.
// Without that cap the transport starts extra background dials whenever a
// request waits for a connection; against a target that refuses
// connections those dials pile up, and on Windows each one blocked in
// socket creation holds an OS thread. A 1,000-VU run crashed that way with
// "thread exhaustion" after exceeding Go's 10,000-thread limit.
//
// Redirects are not followed, so each request is measured on its own.
//
// Only headers the script sets are sent: Go's transport would otherwise add
// "Accept-Encoding: gzip" and decompress responses, which k6 and JMeter do
// not do and which changes the work the server and the client perform.
func New(maxConnsPerHost int, timeout time.Duration) *http.Client {
	return NewWithOptions(Options{MaxConnsPerHost: maxConnsPerHost, Timeout: timeout})
}

// Options configure NewWithOptions.
type Options struct {
	MaxConnsPerHost int
	Timeout         time.Duration
	// NoConnectionReuse disables keep-alive: every request opens a new
	// connection (options.noConnectionReuse, ADR-009).
	NoConnectionReuse bool
	// HTTPVersion selects the protocols (ADR-010): HTTPAuto (also the
	// zero value), HTTP11 or HTTP2.
	HTTPVersion string
	// TLSConfig, if set, replaces the default TLS settings; tests use it
	// to trust a test server's certificate.
	TLSConfig *tls.Config
}

// HTTP versions for Options.HTTPVersion and options.httpVersion.
const (
	// HTTPAuto uses HTTP/2 over TLS when the server offers it (ALPN) and
	// HTTP/1.1 otherwise, as k6 does.
	HTTPAuto = "auto"
	// HTTP11 always uses HTTP/1.1 (the Phase 0 behaviour).
	HTTP11 = "1.1"
	// HTTP2 only uses HTTP/2: over TLS, or h2c (prior knowledge) for
	// http:// URLs. A TLS server without HTTP/2 fails the request.
	HTTP2 = "2"
)

// protocols returns the transport protocols for an HTTP version.
func protocols(version string) *http.Protocols {
	var p http.Protocols
	switch version {
	case HTTP11:
		p.SetHTTP1(true)
	case HTTP2:
		// Without HTTP1, http:// URLs use unencrypted HTTP/2.
		p.SetHTTP2(true)
		p.SetUnencryptedHTTP2(true)
	default: // HTTPAuto
		p.SetHTTP1(true)
		p.SetHTTP2(true)
	}
	return &p
}

// NewWithOptions is New with every setting (see New).
func NewWithOptions(o Options) *http.Client {
	maxConnsPerHost, timeout := o.MaxConnsPerHost, o.Timeout
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = o.NoConnectionReuse
	t.MaxConnsPerHost = maxConnsPerHost
	t.MaxIdleConns = maxConnsPerHost
	t.MaxIdleConnsPerHost = maxConnsPerHost
	t.DisableCompression = true
	t.Protocols = protocols(o.HTTPVersion)
	if o.TLSConfig != nil {
		t.TLSClientConfig = o.TLSConfig.Clone()
	}

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
	// KeepBody keeps the response body in Result.Body. Otherwise it is
	// read and discarded, which allocates nothing per request.
	KeepBody bool
}

// Result is the outcome of one request.
type Result struct {
	// Status is the HTTP status code, or 0 if no response was received.
	Status int
	// Duration covers sending the request and reading the whole body.
	Duration time.Duration
	Err      error
	// Header is the response's header, nil if no response was received.
	Header http.Header
	// Proto is the protocol of the response, such as "HTTP/1.1" or
	// "HTTP/2.0"; empty if no response was received.
	Proto string
	// Body is the response body when Request.KeepBody is set. It is
	// non-nil (possibly empty) whenever a body was kept, so callers can
	// tell an empty body from a discarded one.
	Body []byte
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
		rec.RecordUnsent()
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
		res.Header = resp.Header
		res.Proto = resp.Proto
		// Read the body to the end either way, so the connection returns
		// to the pool and Duration includes the transfer.
		if r.KeepBody {
			res.Body, err = readBody(resp)
		} else {
			_, err = io.Copy(io.Discard, resp.Body)
		}
		resp.Body.Close()
	}
	res.Duration = time.Since(start)
	res.Err = err

	if ctx.Err() == nil {
		rec.Record(res.Duration, res.OK())
	}
	return res
}

// readBody reads the whole body, in one allocation when the server sends
// Content-Length.
func readBody(resp *http.Response) ([]byte, error) {
	// net/http never returns more than Content-Length bytes, so reading
	// exactly that many reaches the end of the body.
	if n := resp.ContentLength; n >= 0 && n <= maxPresize {
		b := make([]byte, n)
		if _, err := io.ReadFull(resp.Body, b); err != nil {
			return nil, err
		}
		return b, nil
	}
	b, err := io.ReadAll(resp.Body)
	if b == nil && err == nil {
		b = []byte{}
	}
	return b, err
}

// maxPresize bounds the buffer allocated up front from Content-Length, so a
// wrong header cannot make one request allocate a huge buffer.
const maxPresize = 16 << 20
