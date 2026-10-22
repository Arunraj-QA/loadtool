package ui

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/netguard"
	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/runner"
)

//go:embed run.ts
var runScript []byte

// checkName is the check the fixed script runs on every response.
const checkName = "expected status"

// Run states.
const (
	stateRunning   = "running"
	stateCompleted = "completed"
	stateFailed    = "failed"
	stateCancelled = "cancelled"
)

// Options configure a Controller.
type Options struct {
	Limits
	// Version goes into the reports, as with loadtool run.
	Version string
	// Log receives one line when a run starts and ends; never request
	// details beyond the method and URL without its query.
	Log io.Writer
	// run is runner.Run; tests replace it.
	run func(context.Context, runner.Params) (report.Result, error)
}

// Controller runs one load test at a time.
type Controller struct {
	opts Options

	mu  sync.Mutex
	cur *run // the latest run; nil before the first
}

// run is one load test and what it produced.
type run struct {
	id      string
	label   string
	vus     int
	dur     time.Duration
	started time.Time
	cancel  context.CancelFunc
	redact  redactor

	mu                     sync.Mutex
	state                  string
	ended                  time.Time
	cancelled              bool
	points                 []metrics.Point
	errMsg                 string
	warnings               []string
	reportJSON, reportHTML []byte
}

// NewController returns a controller with opts; zero limits get the
// defaults (50 VUs, 5 minutes, 10 s per request).
func NewController(opts Options) *Controller {
	if opts.MaxVUs == 0 {
		opts.MaxVUs = 50
	}
	if opts.MaxDuration == 0 {
		opts.MaxDuration = 5 * time.Minute
	}
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 10 * time.Second
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.run == nil {
		opts.run = runner.Run
	}
	return &Controller{opts: opts}
}

var errBusy = errors.New("a test is already running; cancel it or wait for it to end")

// Start validates c and starts a run. It returns the run's ID.
func (ctl *Controller) Start(c RunConfig) (string, error) {
	p, err := validate(c, ctl.opts.Limits)
	if err != nil {
		return "", err
	}
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.cur != nil && ctl.cur.status() == stateRunning {
		return "", errBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		id: newID(), label: p.Label, vus: p.VUs, dur: p.Duration, started: time.Now(),
		cancel: cancel, redact: newRedactor(p.Secrets), state: stateRunning,
	}
	ctl.cur = r
	fmt.Fprintf(ctl.opts.Log, "ui: run %s started: %s, %d VUs for %s\n", r.id, r.label, r.vus, r.dur)
	go ctl.execute(ctx, r, p)
	return r.id, nil
}

// execute runs p through the runner and stores the outcome in r.
func (ctl *Controller) execute(ctx context.Context, r *run, p plan) {
	defer r.cancel()
	res, err := ctl.runPlan(ctx, r, p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = time.Now()
	switch {
	case err != nil:
		r.state, r.errMsg = stateFailed, r.redact.text(err.Error())
	case r.cancelled:
		r.state = stateCancelled
	default:
		r.state = stateCompleted
	}
	if err == nil {
		res.Script = p.Label // not the temporary file
		var j, h bytes.Buffer
		if e := report.JSON(&j, res, ctl.opts.Version); e == nil {
			r.reportJSON = r.redact.bytes(j.Bytes())
		}
		if e := report.HTML(&h, res, ctl.opts.Version); e == nil {
			r.reportHTML = r.redact.bytes(h.Bytes())
		}
	}
	fmt.Fprintf(ctl.opts.Log, "ui: run %s %s\n", r.id, r.state)
}

func (ctl *Controller) runPlan(ctx context.Context, r *run, p plan) (report.Result, error) {
	dir, err := os.MkdirTemp("", "loadtool-ui-")
	if err != nil {
		return report.Result{}, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "ui-run.ts")
	if err := os.WriteFile(path, runScript, 0o600); err != nil {
		return report.Result{}, err
	}
	cfgJSON, err := json.Marshal(map[string]any{
		"method": p.Method, "url": p.URL, "body": p.Body, "checkName": checkName,
		"minStatus": p.MinStatus, "maxStatus": p.MaxStatus, "thresholds": p.Thresholds,
	})
	if err != nil {
		return report.Result{}, err
	}
	headers := map[string]string{}
	for k, v := range p.Header {
		headers[k] = v[0]
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return report.Result{}, err
	}
	params := runner.Params{
		Config:    config.Config{Script: path, GracefulStop: ctl.opts.RequestTimeout},
		Overrides: config.Overrides{VUs: &p.VUs, Duration: &p.Duration},
		// Credentials travel only here: __ENV is never written to
		// reports.
		Env:            map[string]string{"LOADTOOL_UI_RUN": string(cfgJSON), "LOADTOOL_UI_HEADERS": string(headersJSON)},
		Console:        io.Discard,
		Warn:           func(msg string) { r.warn(msg) },
		NoProxy:        true,
		RequestTimeout: ctl.opts.RequestTimeout,
		OnSample:       r.addPoint,
	}
	if !ctl.opts.AllowPrivate {
		params.DialControl = netguard.Control
	}
	return ctl.opts.run(ctx, params)
}

// Cancel stops the run with ID id, if it is running.
func (ctl *Controller) Cancel(id string) error {
	r := ctl.get(id)
	if r == nil {
		return errNotFound
	}
	r.mu.Lock()
	if r.state == stateRunning {
		r.cancelled = true
	}
	r.mu.Unlock()
	r.cancel()
	return nil
}

var errNotFound = errors.New("no such run")

func (ctl *Controller) get(id string) *run {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if ctl.cur == nil || ctl.cur.id != id {
		return nil
	}
	return ctl.cur
}

func (r *run) status() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *run) addPoint(p metrics.Point) {
	r.mu.Lock()
	r.points = append(r.points, p)
	r.mu.Unlock()
}

func (r *run) warn(msg string) {
	r.mu.Lock()
	if len(r.warnings) < 20 {
		r.warnings = append(r.warnings, r.redact.text(msg))
	}
	r.mu.Unlock()
}

// Status is what the page polls. Numbers come from the engine's
// time-series points while running, and from the JSON report at the end.
type Status struct {
	ID            string          `json:"id"`
	State         string          `json:"state"`
	Label         string          `json:"label"`
	StartedAt     string          `json:"startedAt"`
	ElapsedMs     float64         `json:"elapsedMs"`
	DurationMs    float64         `json:"durationMs"`
	ConfiguredVUs int             `json:"configuredVUs"`
	ActiveVUs     int             `json:"activeVUs"`
	Live          live            `json:"live"`
	Error         string          `json:"error,omitempty"`
	Warnings      []string        `json:"warnings,omitempty"`
	Final         json.RawMessage `json:"final,omitempty"` // the JSON report, when the run ended
}

type live struct {
	Requests int         `json:"requests"`
	Failed   int         `json:"failed"`
	Points   []livePoint `json:"points"`
}

type livePoint struct {
	AtMs     float64 `json:"atMs"`
	Requests int     `json:"requests"`
	Failed   int     `json:"failed"`
	MeanMs   float64 `json:"meanMs"`
	P50Ms    float64 `json:"p50Ms"`
	P95Ms    float64 `json:"p95Ms"`
	P99Ms    float64 `json:"p99Ms"`
	VUs      int     `json:"vus"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Status returns the run's state.
func (ctl *Controller) Status(id string) (Status, error) {
	r := ctl.get(id)
	if r == nil {
		return Status{}, errNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	end := time.Now()
	if !r.ended.IsZero() {
		end = r.ended
	}
	s := Status{
		ID: r.id, State: r.state, Label: r.label, StartedAt: r.started.UTC().Format(time.RFC3339),
		ElapsedMs: ms(end.Sub(r.started)), DurationMs: ms(r.dur), ConfiguredVUs: r.vus,
		Error: r.errMsg, Warnings: r.warnings, Live: live{Points: []livePoint{}},
	}
	for _, p := range r.points {
		s.Live.Requests += p.Requests
		s.Live.Failed += p.Failed
		s.Live.Points = append(s.Live.Points, livePoint{
			AtMs: ms(p.At), Requests: p.Requests, Failed: p.Failed,
			MeanMs: ms(p.Mean), P50Ms: ms(p.P50), P95Ms: ms(p.P95), P99Ms: ms(p.P99), VUs: p.VUs,
		})
	}
	if n := len(r.points); n > 0 && r.state == stateRunning {
		s.ActiveVUs = r.points[n-1].VUs
	}
	if r.reportJSON != nil {
		s.Final = r.reportJSON
	}
	return s, nil
}

// Report returns the run's JSON or HTML report, once it has ended.
func (ctl *Controller) Report(id, kind string) ([]byte, error) {
	r := ctl.get(id)
	if r == nil {
		return nil, errNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.reportJSON
	if kind == "html" {
		b = r.reportHTML
	}
	if b == nil {
		return nil, errors.New("the report is ready when the run ends")
	}
	return b, nil
}

// Preview is one preflight request's outcome.
type Preview struct {
	Status     int               `json:"status"`
	Proto      string            `json:"proto,omitempty"`
	DurationMs float64           `json:"durationMs"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"` // the first 2 KB
	Truncated  bool              `json:"truncated,omitempty"`
	Error      string            `json:"error,omitempty"`
}

const previewBytes = 2 << 10

// Preflight sends the request once, through the same HTTP client
// settings as a run, and records nothing.
func (ctl *Controller) Preflight(ctx context.Context, a APIRequest) (Preview, error) {
	req, err := validateRequest(a, ctl.opts.Limits)
	if err != nil {
		return Preview{}, err
	}
	opts := httpclient.Options{MaxConnsPerHost: 1, Timeout: ctl.opts.RequestTimeout, NoProxy: true}
	if !ctl.opts.AllowPrivate {
		opts.DialControl = netguard.Control
	}
	client := httpclient.NewWithOptions(opts)
	defer client.CloseIdleConnections()
	res, _ := httpclient.Send(ctx, client, httpclient.Request{Method: req.Method, URL: req.URL, Body: req.Body, Header: req.Header, KeepBody: true})
	red := newRedactor(req.Secrets)
	pv := Preview{Status: res.Status, Proto: res.Proto, DurationMs: ms(res.Duration)}
	if res.Err != nil {
		pv.Error = red.text(res.Err.Error())
		return pv, nil
	}
	pv.Headers = map[string]string{}
	for k, v := range res.Header {
		pv.Headers[k] = red.text(v[0])
	}
	body := res.Body
	if len(body) > previewBytes {
		body, pv.Truncated = body[:previewBytes], true
	}
	pv.Body = red.text(string(body))
	return pv, nil
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Close cancels a running test and waits up to timeout for it to end.
func (ctl *Controller) Close(timeout time.Duration) {
	ctl.mu.Lock()
	r := ctl.cur
	ctl.mu.Unlock()
	if r == nil {
		return
	}
	if r.status() == stateRunning {
		_ = ctl.Cancel(r.id)
	}
	for deadline := time.Now().Add(timeout); r.status() == stateRunning && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
}
