package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/runner"
)

const testToken = "test-token"

// harness is a controller behind its HTTP server, reached as a browser on
// this machine would.
type harness struct {
	t   *testing.T
	ctl *Controller
	srv *httptest.Server
	log *bytes.Buffer
}

func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	log := &safeBuffer{}
	opts.Log = log
	ctl := NewController(opts)
	h := &harness{t: t, ctl: ctl, log: &log.buf}
	h.srv = httptest.NewUnstartedServer(nil)
	h.srv.Start()
	h.srv.Config.Handler = NewServer(ctl, testToken, h.srv.Listener.Addr().String())
	t.Cleanup(func() { ctl.Close(10 * time.Second); h.srv.Close() })
	return h
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// call sends method to path with an optional JSON body; it returns the
// status and the body.
func (h *harness) call(method, path string, body any, edit ...func(*http.Request)) (int, []byte) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	req.Header.Set(tokenHeader, testToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, e := range edit {
		e(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

// wait polls the run until it leaves the running state.
func (h *harness) wait(id string) Status {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		code, body := h.call("GET", "/api/runs/"+id, nil)
		if code != http.StatusOK {
			h.t.Fatalf("status: %d %s", code, body)
		}
		var st Status
		if err := json.Unmarshal(body, &st); err != nil {
			h.t.Fatal(err)
		}
		if st.State != stateRunning {
			return st
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatal("the run did not end")
	return Status{}
}

func (h *harness) start(c RunConfig) string {
	h.t.Helper()
	code, body := h.call("POST", "/api/runs", c)
	if code != http.StatusAccepted {
		h.t.Fatalf("start: %d %s", code, body)
	}
	var r struct{ ID string }
	json.Unmarshal(body, &r)
	return r.ID
}

func target(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func baseRun(url string) RunConfig {
	return RunConfig{Request: APIRequest{Method: "GET", URL: url}, VUs: 2, Duration: "2s", Authorized: true}
}

var local = Limits{MaxVUs: 10, MaxDuration: time.Minute, AllowPrivate: true, RequestTimeout: 2 * time.Second}

func TestValidate(t *testing.T) {
	pub := Limits{MaxVUs: 50, MaxDuration: 5 * time.Minute}
	ok := RunConfig{Request: APIRequest{Method: "post", URL: "https://api.example.com/v1/items?a=1",
		Query:   []Field{{Name: "b", Value: "2"}, {Name: "key", Value: "s3cret", Secret: true}},
		Headers: []Field{{Name: "X-Trace", Value: "t"}}, Body: `{"n": 1}`,
		Auth: Auth{Type: "basic", Username: "u", Password: "pw-123"}}, VUs: 5, Duration: "30s", P95Ms: 250, MaxErrorPct: 1, Authorized: true}
	p, err := validate(ok, pub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Method != "POST" || p.URL != "https://api.example.com/v1/items?a=1&b=2&key=s3cret" || p.Label != "POST https://api.example.com/v1/items" {
		t.Errorf("plan: %s %s (%s)", p.Method, p.URL, p.Label)
	}
	if p.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(p.Header.Get("Authorization"), "Basic ") {
		t.Errorf("headers: %v", p.Header)
	}
	if p.MinStatus != 200 || p.MaxStatus != 299 || p.Thresholds["http_req_duration"][0] != "p(95)<250" || p.Thresholds["http_req_failed"][0] != "rate<0.01" {
		t.Errorf("status range or thresholds: %d-%d %v", p.MinStatus, p.MaxStatus, p.Thresholds)
	}
	for _, s := range []string{"s3cret", "pw-123"} {
		if !strings.Contains(strings.Join(p.Secrets, " "), s) {
			t.Errorf("secret %q not tracked", s)
		}
	}

	bad := map[string]func(*RunConfig){
		"method":              func(c *RunConfig) { c.Request.Method = "TRACE" },
		"no scheme":           func(c *RunConfig) { c.Request.URL = "api.example.com" },
		"loopback":            func(c *RunConfig) { c.Request.URL = "http://127.0.0.1:8080/" },
		"localhost":           func(c *RunConfig) { c.Request.URL = "http://localhost/" },
		"metadata":            func(c *RunConfig) { c.Request.URL = "http://169.254.169.254/latest" },
		"private":             func(c *RunConfig) { c.Request.URL = "http://192.168.1.10/" },
		"user info":           func(c *RunConfig) { c.Request.URL = "https://u:p@api.example.com/" },
		"fragment":            func(c *RunConfig) { c.Request.URL = "https://api.example.com/#x" },
		"header name":         func(c *RunConfig) { c.Request.Headers = []Field{{Name: "Bad Name", Value: "v"}} },
		"header line break":   func(c *RunConfig) { c.Request.Headers = []Field{{Name: "X-A", Value: "v\r\nX-B: w"}} },
		"forbidden header":    func(c *RunConfig) { c.Request.Headers = []Field{{Name: "host", Value: "evil"}} },
		"empty query name":    func(c *RunConfig) { c.Request.Query = []Field{{Name: " ", Value: "v"}} },
		"invalid JSON":        func(c *RunConfig) { c.Request.Body = `{"a":` },
		"body on GET":         func(c *RunConfig) { c.Request.Method = "GET" },
		"huge body":           func(c *RunConfig) { c.Request.Body = `"` + strings.Repeat("x", maxBodyBytes) + `"` },
		"empty bearer":        func(c *RunConfig) { c.Request.Auth = Auth{Type: "bearer"} },
		"basic colon":         func(c *RunConfig) { c.Request.Auth = Auth{Type: "basic", Username: "a:b"} },
		"auth type":           func(c *RunConfig) { c.Request.Auth = Auth{Type: "digest"} },
		"zero VUs":            func(c *RunConfig) { c.VUs = 0 },
		"too many VUs":        func(c *RunConfig) { c.VUs = 51 },
		"duration format":     func(c *RunConfig) { c.Duration = "30" },
		"too short":           func(c *RunConfig) { c.Duration = "500ms" },
		"too long":            func(c *RunConfig) { c.Duration = "6m" },
		"status range":        func(c *RunConfig) { c.MinStatus, c.MaxStatus = 300, 200 },
		"negative threshold":  func(c *RunConfig) { c.P95Ms = -1 },
		"error rate over 100": func(c *RunConfig) { c.MaxErrorPct = 101 },
		"not authorized":      func(c *RunConfig) { c.Authorized = false },
	}
	for name, edit := range bad {
		c := ok
		c.Request.Query = append([]Field(nil), ok.Request.Query...)
		edit(&c)
		if _, err := validate(c, pub); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRedactor(t *testing.T) {
	r := newRedactor([]string{"ab", "token-123", "token-12345"})
	if got := r.text("x token-12345 y token-123 z ab"); got != "x *** y *** z ab" {
		t.Errorf("redacted %q", got)
	}
}

// The controller answers only this machine's browser: the expected Host,
// no foreign Origin, the session token, and JSON bodies.
func TestServerRefusesOtherClients(t *testing.T) {
	h := newHarness(t, Options{Limits: local})
	cases := []struct {
		name string
		edit func(*http.Request)
		want int
	}{
		{"wrong Host (DNS rebinding)", func(r *http.Request) { r.Host = "attacker.example:80" }, http.StatusMisdirectedRequest},
		{"foreign Origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, http.StatusForbidden},
		{"no token", func(r *http.Request) { r.Header.Del(tokenHeader) }, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set(tokenHeader, "guess") }, http.StatusUnauthorized},
		{"form body", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		if code, _ := h.call("POST", "/api/runs", baseRun("http://example.com"), c.edit); code != c.want {
			t.Errorf("%s: %d, want %d", c.name, code, c.want)
		}
	}
	if code, body := h.call("POST", "/api/runs", map[string]any{"vus": 1, "unknown": true}); code != http.StatusBadRequest {
		t.Errorf("unknown field: %d %s", code, body)
	}
	// The page itself loads, with a restrictive policy.
	res, err := http.Get(h.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), `id="form"`) || !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("page: %d, CSP %q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}
}

// A run goes through the real runner: live points while it runs, and at
// the end the JSON report, whose numbers agree with the points, plus the
// status codes, the check and the thresholds. The downloads are the same
// reports, labelled with the request rather than a temporary file.
func TestRunEndToEnd(t *testing.T) {
	var n sync.Mutex
	count := 0
	api := target(t, func(w http.ResponseWriter, r *http.Request) {
		n.Lock()
		count++
		c := count
		n.Unlock()
		if c%5 == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	h := newHarness(t, Options{Limits: local, Version: "test"})
	c := baseRun(api.URL + "/items?q=1")
	c.P95Ms, c.MaxErrorPct = 1000, 50
	id := h.start(c)
	st := h.wait(id)
	if st.State != stateCompleted || st.Error != "" {
		t.Fatalf("state %s, error %q", st.State, st.Error)
	}
	var rep struct {
		Script  string
		Metrics struct {
			HTTPReqs        struct{ Count int } `json:"http_reqs"`
			HTTPStatusCodes map[string]int      `json:"http_status_codes"`
		}
		Checks     []struct{ Name string }
		Thresholds []struct{ Passed bool }
	}
	if err := json.Unmarshal(st.Final, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Script != "GET "+api.URL+"/items" {
		t.Errorf("report script %q, want the request label", rep.Script)
	}
	if rep.Metrics.HTTPReqs.Count == 0 || rep.Metrics.HTTPReqs.Count != st.Live.Requests {
		t.Errorf("report %d requests, live points %d; want equal and > 0", rep.Metrics.HTTPReqs.Count, st.Live.Requests)
	}
	codes := rep.Metrics.HTTPStatusCodes
	if codes["200"] == 0 || codes["503"] == 0 || codes["200"]+codes["503"] != rep.Metrics.HTTPReqs.Count {
		t.Errorf("status codes %v for %d requests", codes, rep.Metrics.HTTPReqs.Count)
	}
	if len(rep.Checks) != 1 || rep.Checks[0].Name != checkName || len(rep.Thresholds) != 2 {
		t.Errorf("checks %v, %d thresholds", rep.Checks, len(rep.Thresholds))
	}
	if len(st.Live.Points) == 0 || st.ConfiguredVUs != 2 {
		t.Errorf("%d live points, %d configured VUs", len(st.Live.Points), st.ConfiguredVUs)
	}
	// The status embeds the report compacted; the download is the
	// report as written. Same content.
	code, js := h.call("GET", "/api/runs/"+id+"/report.json", nil)
	var dl, fin any
	json.Unmarshal(js, &dl)
	json.Unmarshal(st.Final, &fin)
	if code != 200 || !reflect.DeepEqual(dl, fin) {
		t.Errorf("JSON download: %d, same as the final report: %v", code, reflect.DeepEqual(dl, fin))
	}
	code, html := h.call("GET", "/api/runs/"+id+"/report.html", nil)
	if code != 200 || !strings.Contains(string(html), "<!doctype html>") || strings.Contains(string(html), "ui-run.ts") {
		t.Errorf("HTML download: %d, mentions the temporary script: %v", code, strings.Contains(string(html), "ui-run.ts"))
	}
	if !strings.Contains(h.log.String(), "completed") {
		t.Errorf("log: %q", h.log.String())
	}
}

// Without --allow-private-targets, a local target is refused before the
// run (and the runner's dialer would refuse it too).
func TestPublicOnlyByDefault(t *testing.T) {
	api := target(t, func(http.ResponseWriter, *http.Request) {})
	h := newHarness(t, Options{Limits: Limits{MaxVUs: 10, MaxDuration: time.Minute}})
	for _, url := range []string{api.URL, strings.Replace(api.URL, "127.0.0.1", "localhost", 1)} {
		if code, body := h.call("POST", "/api/runs", baseRun(url)); code != http.StatusBadRequest || !strings.Contains(string(body), "refused") {
			t.Errorf("%s: %d %s", url, code, body)
		}
		if code, _ := h.call("POST", "/api/preflight", APIRequest{Method: "GET", URL: url}); code != http.StatusBadRequest {
			t.Errorf("preflight %s: %d", url, code)
		}
	}
}

// Cancel stops a running test promptly; its partial results are kept.
// Only one test runs at a time.
func TestCancelAndOneAtATime(t *testing.T) {
	api := target(t, func(http.ResponseWriter, *http.Request) { time.Sleep(5 * time.Millisecond) })
	h := newHarness(t, Options{Limits: local})
	c := baseRun(api.URL)
	c.Duration = "30s"
	id := h.start(c)
	if code, body := h.call("POST", "/api/runs", c); code != http.StatusConflict {
		t.Errorf("second run: %d %s, want 409", code, body)
	}
	time.Sleep(1200 * time.Millisecond)
	start := time.Now()
	if code, _ := h.call("POST", "/api/runs/"+id+"/cancel", struct{}{}); code != http.StatusNoContent {
		t.Fatalf("cancel: %d", code)
	}
	st := h.wait(id)
	if st.State != stateCancelled || st.Final == nil || time.Since(start) > 10*time.Second {
		t.Errorf("state %s, final report %v, took %v", st.State, st.Final != nil, time.Since(start))
	}
	if code, _ := h.call("GET", "/api/runs/nope", nil); code != http.StatusNotFound {
		t.Errorf("unknown run: %d", code)
	}
}

// A runner error makes the run failed, with the message redacted.
func TestFailedRun(t *testing.T) {
	h := newHarness(t, Options{Limits: local, run: func(context.Context, runner.Params) (report.Result, error) {
		return report.Result{}, errors.New("boom near tok-secret-99")
	}})
	c := baseRun("http://example.com/")
	c.Request.Auth = Auth{Type: "bearer", Token: "tok-secret-99"}
	st := h.wait(h.start(c))
	if st.State != stateFailed || st.Error != "boom near ***" || st.Final != nil {
		t.Errorf("state %s, error %q, final %v", st.State, st.Error, st.Final != nil)
	}
	if code, _ := h.call("GET", "/api/runs/"+st.ID+"/report.json", nil); code != http.StatusConflict {
		t.Errorf("report of a failed run: %d, want 409", code)
	}
}

// Secrets reach the API, and nowhere else: not the status, the reports,
// the preflight preview (even when the server echoes them) or the log.
func TestSecretsStayOut(t *testing.T) {
	const bearer, apiKey, queryKey, password = "bearer-SECRET-1", "header-SECRET-2", "query-SECRET-3", "basic-SECRET-4"
	var mu sync.Mutex
	seen := map[string]bool{}
	api := target(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get("Authorization")] = true
		seen[r.Header.Get("X-Api-Key")] = true
		seen[r.URL.Query().Get("key")] = true
		mu.Unlock()
		w.Header().Set("X-Echo", r.Header.Get("X-Api-Key"))
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, "auth=%s key=%s query=%s", r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.URL.RawQuery)
	})
	h := newHarness(t, Options{Limits: local})
	req := APIRequest{Method: "GET", URL: api.URL + "/private",
		Query:   []Field{{Name: "key", Value: queryKey, Secret: true}},
		Headers: []Field{{Name: "X-Api-Key", Value: apiKey, Secret: true}},
		Auth:    Auth{Type: "bearer", Token: bearer}}
	var outputs []string
	code, pv := h.call("POST", "/api/preflight", req)
	if code != 200 {
		t.Fatalf("preflight: %d %s", code, pv)
	}
	outputs = append(outputs, string(pv))
	c := baseRun("")
	c.Request = req
	id := h.start(c)
	st := h.wait(id)
	stJSON, _ := json.Marshal(st)
	_, js := h.call("GET", "/api/runs/"+id+"/report.json", nil)
	_, html := h.call("GET", "/api/runs/"+id+"/report.html", nil)
	outputs = append(outputs, string(stJSON), string(js), string(html), h.log.String())

	req.Auth = Auth{Type: "basic", Username: "user", Password: password}
	code, pv = h.call("POST", "/api/preflight", req)
	outputs = append(outputs, string(pv))
	if code != 200 || !strings.Contains(string(pv), "***") {
		t.Errorf("basic preflight: %d %s", code, pv)
	}
	for _, secret := range []string{bearer, apiKey, queryKey, password} {
		for i, out := range outputs {
			if strings.Contains(out, secret) {
				t.Errorf("output %d contains %q", i, secret)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !seen["Bearer "+bearer] || !seen[apiKey] || !seen[queryKey] {
		t.Errorf("the API did not receive the credentials: %v", seen)
	}
}
