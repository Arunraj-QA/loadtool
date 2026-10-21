// Package ui is the local controller behind `loadtool ui` (ADR-023): a
// page and a JSON API on the loopback interface that run a load test
// against one HTTP API through the existing runner, without a script.
package ui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/netguard"
)

// Limits on what the form may ask for.
const (
	maxBodyBytes = 256 << 10
	maxURLBytes  = 8 << 10
	maxFields    = 50 // query parameters, and headers
)

// Field is a query parameter or a header. Secret values are masked in
// the page and replaced with *** in everything the controller shows.
type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// Auth is the request's authentication.
type Auth struct {
	Type     string `json:"type"` // "", "none", "bearer" or "basic"
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// APIRequest is the request the test sends.
type APIRequest struct {
	Method  string  `json:"method"`
	URL     string  `json:"url"`
	Query   []Field `json:"query"`
	Headers []Field `json:"headers"`
	// Body is JSON text; empty means no body.
	Body string `json:"body"`
	Auth Auth   `json:"auth"`
}

// RunConfig is a load test, as the page submits it.
type RunConfig struct {
	Request  APIRequest `json:"request"`
	VUs      int        `json:"vus"`
	Duration string     `json:"duration"` // a Go duration, such as "30s"
	// MinStatus and MaxStatus are the status codes the check accepts;
	// 0 and 0 mean 200 to 299.
	MinStatus int `json:"minStatus"`
	MaxStatus int `json:"maxStatus"`
	// P95Ms and MaxErrorPct are optional thresholds; 0 means none.
	P95Ms       float64 `json:"p95Ms"`
	MaxErrorPct float64 `json:"maxErrorPct"`
	// Authorized must be true: the user confirms they may load-test the
	// API.
	Authorized bool `json:"authorized"`
}

// Limits bound what a run may do.
type Limits struct {
	MaxVUs         int
	MaxDuration    time.Duration
	AllowPrivate   bool // allow loopback and private destinations (local testing)
	RequestTimeout time.Duration
}

// request is a validated request: what the fixed script sends.
type request struct {
	Method string      `json:"method"`
	URL    string      `json:"url"`
	Body   string      `json:"body"`
	Header http.Header `json:"-"`
	// Label is the method and URL without its query: what reports and
	// logs show.
	Label string `json:"-"`
	// Secrets are values to replace with *** wherever they could appear.
	Secrets []string `json:"-"`
}

// plan is a validated run.
type plan struct {
	request
	VUs                  int
	Duration             time.Duration
	MinStatus, MaxStatus int
	Thresholds           map[string][]string
}

var methods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// forbiddenHeaders are set by the HTTP client, not the user.
var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Proxy-Authorization": true, "Proxy-Connection": true,
}

// validateRequest checks a request and builds what the script sends.
func validateRequest(r APIRequest, lim Limits) (request, error) {
	var out request
	out.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if !methods[out.Method] {
		return out, fmt.Errorf("method must be GET, POST, PUT, PATCH or DELETE")
	}
	raw := strings.TrimSpace(r.URL)
	if len(raw) > maxURLBytes {
		return out, fmt.Errorf("the URL is longer than %d bytes", maxURLBytes)
	}
	u, err := checkURL(raw, lim.AllowPrivate)
	if err != nil {
		return out, err
	}
	if u.Fragment != "" {
		return out, fmt.Errorf("remove the #fragment from the URL")
	}
	if len(r.Query) > maxFields || len(r.Headers) > maxFields {
		return out, fmt.Errorf("at most %d query parameters and %d headers", maxFields, maxFields)
	}
	q := u.Query()
	for _, f := range r.Query {
		if strings.TrimSpace(f.Name) == "" {
			return out, fmt.Errorf("a query parameter has no name")
		}
		q.Add(f.Name, f.Value)
		if f.Secret {
			out.Secrets = append(out.Secrets, f.Value, url.QueryEscape(f.Value))
		}
	}
	u.RawQuery = q.Encode()
	out.URL = u.String()
	out.Label = out.Method + " " + (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()

	out.Header = http.Header{}
	for _, f := range r.Headers {
		name := strings.TrimSpace(f.Name)
		if !validHeaderName(name) {
			return out, fmt.Errorf("invalid header name %q", name)
		}
		if forbiddenHeaders[http.CanonicalHeaderKey(name)] {
			return out, fmt.Errorf("the %s header is set by LoadTool", http.CanonicalHeaderKey(name))
		}
		if strings.ContainsAny(f.Value, "\r\n") {
			return out, fmt.Errorf("header %s: a value cannot contain a line break", name)
		}
		out.Header.Add(name, f.Value)
		if f.Secret {
			out.Secrets = append(out.Secrets, f.Value)
		}
	}
	if err := applyAuth(&out, r.Auth); err != nil {
		return out, err
	}

	if body := strings.TrimSpace(r.Body); body != "" {
		if out.Method == "GET" {
			return out, fmt.Errorf("a GET request cannot have a body")
		}
		if len(body) > maxBodyBytes {
			return out, fmt.Errorf("the body is larger than %d KB", maxBodyBytes>>10)
		}
		if !json.Valid([]byte(body)) {
			return out, fmt.Errorf("the body is not valid JSON")
		}
		out.Body = body
		if out.Header.Get("Content-Type") == "" {
			out.Header.Set("Content-Type", "application/json")
		}
	}
	return out, nil
}

func applyAuth(out *request, a Auth) error {
	switch a.Type {
	case "", "none":
		return nil
	case "bearer":
		tok := strings.TrimSpace(a.Token)
		if tok == "" || strings.ContainsAny(tok, "\r\n ") {
			return fmt.Errorf("the bearer token is empty or contains spaces or line breaks")
		}
		out.Header.Set("Authorization", "Bearer "+tok)
		out.Secrets = append(out.Secrets, tok)
	case "basic":
		if a.Username == "" || strings.ContainsAny(a.Username, ":\r\n") || strings.ContainsAny(a.Password, "\r\n") {
			return fmt.Errorf("basic authentication needs a user name without ':' and no line breaks")
		}
		enc := base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))
		out.Header.Set("Authorization", "Basic "+enc)
		out.Secrets = append(out.Secrets, a.Password, enc)
	default:
		return fmt.Errorf("authentication must be none, bearer or basic")
	}
	return nil
}

// checkURL is netguard.CheckURL, or, with allowPrivate, only its shape
// checks.
func checkURL(raw string, allowPrivate bool) (*url.URL, error) {
	if !allowPrivate {
		return netguard.CheckURL(raw)
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return nil, fmt.Errorf("invalid URL: %w", err)
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("the URL must start with http:// or https://")
	case u.User != nil:
		return nil, errors.New("put credentials in the authentication settings, not in the URL")
	case u.Hostname() == "":
		return nil, errors.New("the URL has no host")
	}
	return u, nil
}

// validHeaderName reports whether name is an HTTP token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c > 0x7e || c <= ' ' || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, c) {
			return false
		}
	}
	return true
}

// validate checks a run against the limits.
func validate(c RunConfig, lim Limits) (plan, error) {
	req, err := validateRequest(c.Request, lim)
	if err != nil {
		return plan{}, err
	}
	p := plan{request: req, VUs: c.VUs, MinStatus: c.MinStatus, MaxStatus: c.MaxStatus}
	if !c.Authorized {
		return p, errors.New("confirm that you are authorized to load-test this API")
	}
	if c.VUs < 1 || c.VUs > lim.MaxVUs {
		return p, fmt.Errorf("VUs must be 1 to %d", lim.MaxVUs)
	}
	if p.Duration, err = time.ParseDuration(strings.TrimSpace(c.Duration)); err != nil {
		return p, fmt.Errorf("duration: use a value such as 30s or 2m")
	}
	if p.Duration < time.Second || p.Duration > lim.MaxDuration {
		return p, fmt.Errorf("duration must be 1s to %s", lim.MaxDuration)
	}
	if p.MinStatus == 0 && p.MaxStatus == 0 {
		p.MinStatus, p.MaxStatus = 200, 299
	}
	if p.MinStatus < 100 || p.MaxStatus > 599 || p.MinStatus > p.MaxStatus {
		return p, errors.New("expected status must be a range within 100 to 599")
	}
	p.Thresholds = map[string][]string{}
	if c.P95Ms < 0 || c.MaxErrorPct < 0 || c.MaxErrorPct > 100 {
		return p, errors.New("thresholds must be positive, and the error rate at most 100 %")
	}
	if c.P95Ms > 0 {
		p.Thresholds["http_req_duration"] = []string{fmt.Sprintf("p(95)<%g", c.P95Ms)}
	}
	if c.MaxErrorPct > 0 {
		p.Thresholds["http_req_failed"] = []string{fmt.Sprintf("rate<%g", c.MaxErrorPct/100)}
	}
	return p, nil
}
