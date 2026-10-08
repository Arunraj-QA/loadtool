package script

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// Lifecycle runs the parts of a script that run once per test rather than
// once per VU (ADR-008): reading options, setup() and teardown(). It owns
// the VU-0 runtime: the script's top-level code runs there once, and
// setup and teardown then run in that same runtime, so module state set
// by setup is visible to teardown.
//
// A Lifecycle is not safe for concurrent use. The runner calls it from one
// goroutine, before and after the load phase.
type Lifecycle struct {
	vu *VU
}

// NewLifecycle runs the script's top-level code once as VU 0. If ctx ends
// while it runs (Ctrl+C), the code is interrupted and ctx's error is
// returned.
func (p *Program) NewLifecycle(ctx context.Context) (*Lifecycle, error) {
	// Response bodies are always kept here: setup typically reads a token
	// from a response, whatever options.discardResponseBodies says.
	c := *p
	c.discardBodies = false
	c.setupData = nil
	vu, err := c.NewVU(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	return &Lifecycle{vu: vu}, nil
}

// Options returns the script's `export const options` as JSON, or nil if
// the script has none. Values JSON cannot represent, such as functions,
// are dropped.
func (l *Lifecycle) Options() ([]byte, error) {
	rt := l.vu.rt
	if !isSet(rt.Get(optionsGlobal)) {
		return nil, nil
	}
	v, err := rt.RunString("JSON.stringify(globalThis." + optionsGlobal + ")")
	if err != nil {
		return nil, fmt.Errorf("options: %s", scriptErrorMessage(err))
	}
	return []byte(v.String()), nil
}

// HasDefault reports whether the script exports a default function.
func (l *Lifecycle) HasDefault() bool { return l.function(defaultExportGlobal) != nil }

// HasSetup and HasTeardown report whether the script exports them.
func (l *Lifecycle) HasSetup() bool    { return l.function(setupGlobal) != nil }
func (l *Lifecycle) HasTeardown() bool { return l.function(teardownGlobal) != nil }

func (l *Lifecycle) function(global string) goja.Callable {
	fn, _ := goja.AssertFunction(l.vu.rt.Get(global))
	return fn
}

// Setup calls the script's setup() once and returns its result as JSON for
// WithSetupData. It returns nil data when the script exports no setup or
// setup returns undefined.
//
// setup may make HTTP requests with client, sleep and run checks; none of
// them count in the test results. It is interrupted when ctx ends or
// timeout passes. A setup that throws, times out or returns a value JSON
// cannot represent (a cycle, a BigInt) is an error: the caller must not
// start the load phase or run teardown.
func (l *Lifecycle) Setup(ctx context.Context, client *http.Client, timeout time.Duration) ([]byte, error) {
	fn := l.function(setupGlobal)
	if fn == nil {
		return nil, nil
	}
	v, err := l.call(ctx, client, timeout, "setup", "setupTimeout", fn)
	if err != nil {
		return nil, err
	}
	if !isSet(v) {
		return nil, nil
	}
	stringify, _ := goja.AssertFunction(l.vu.rt.Get("JSON").ToObject(l.vu.rt).Get("stringify"))
	s, err := stringify(goja.Undefined(), v)
	if err != nil {
		return nil, fmt.Errorf("setup: the returned data must be JSON-serializable: %s", scriptErrorMessage(err))
	}
	if goja.IsUndefined(s) { // a function, for example
		return nil, nil
	}
	return []byte(s.String()), nil
}

// Teardown calls the script's teardown(data) once, with data as returned
// by Setup, and does nothing if the script exports no teardown. data is
// parsed again, so teardown sees exactly what the VUs saw.
//
// Like setup, its requests and checks do not count in the test results.
// It is interrupted when ctx ends or timeout passes.
func (l *Lifecycle) Teardown(ctx context.Context, client *http.Client, timeout time.Duration, data []byte) error {
	fn := l.function(teardownGlobal)
	if fn == nil {
		return nil
	}
	arg, err := l.vu.parseData(data)
	if err != nil {
		return fmt.Errorf("teardown: %w", err)
	}
	_, err = l.call(ctx, client, timeout, "teardown", "teardownTimeout", fn, arg)
	return err
}

// call runs fn in the lifecycle runtime with a request context bounded by
// ctx and timeout. Results are recorded in a recorder that is thrown away.
func (l *Lifecycle) call(ctx context.Context, client *http.Client, timeout time.Duration,
	name, timeoutOption string, fn goja.Callable, args ...goja.Value) (goja.Value, error) {
	vu := l.vu
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// One jar for setup and teardown, so teardown can end setup's session;
	// it is not shared with the VUs (ADR-009).
	vu.ctx, vu.rec, vu.client = callCtx, &metrics.Recorder{}, httpclient.WithJar(client, &vu.jar)
	release := vu.interruptOn(callCtx)
	v, err := fn(goja.Undefined(), args...)
	release()
	vu.ctx, vu.rec, vu.client = nil, nil, nil

	switch {
	// Checked first: if the test was cancelled while the function ran,
	// it counts as interrupted even if it returned normally. sleep
	// returns early on cancellation and the interrupt arrives
	// asynchronously, so the JavaScript can finish first; treating that
	// as success would start VUs (and later teardown) after Ctrl+C.
	case ctx.Err() != nil:
		return nil, fmt.Errorf("%s interrupted: %w", name, context.Cause(ctx))
	case err == nil:
		return v, nil
	case errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("%s did not finish within %s (%s %s)", name, timeoutOption, timeoutOption, timeout)
	default:
		return nil, fmt.Errorf("%s: %s", name, scriptErrorMessage(err))
	}
}

// interruptOn interrupts the runtime's JavaScript when ctx ends. The
// returned release must be called once the JavaScript has returned. If the
// interrupt fired, possibly just after the code returned, release waits for
// it and clears it; otherwise goja would abort the next call on this
// runtime, such as teardown after a setup that finished at its deadline.
func (vu *VU) interruptOn(ctx context.Context) (release func()) {
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		vu.rt.Interrupt(errStopped)
		close(fired)
	})
	return func() {
		if !stop() {
			<-fired
			vu.rt.ClearInterrupt()
		}
	}
}

// parseData turns setup JSON into a value of this runtime: undefined for
// nil data.
func (vu *VU) parseData(data []byte) (goja.Value, error) {
	if data == nil {
		return goja.Undefined(), nil
	}
	parse, _ := goja.AssertFunction(vu.rt.Get("JSON").ToObject(vu.rt).Get("parse"))
	v, err := parse(goja.Undefined(), vu.rt.ToValue(string(data)))
	if err != nil {
		return nil, fmt.Errorf("parse setup data: %s", scriptErrorMessage(err))
	}
	return v, nil
}

// Close closes the lifecycle runtime's protocol module instances, after
// teardown.
func (l *Lifecycle) Close(ctx context.Context) error {
	return l.vu.Close(ctx)
}
