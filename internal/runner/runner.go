// Package runner orchestrates one test run: it loads the script, resolves
// the run settings, starts the VUs, runs the engine and returns the result
// for the reporters. It knows nothing about the command line; the CLI (or
// any other caller) supplies the inputs and renders the result.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/engine"
	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/script"
)

// Params are the inputs of a run.
type Params struct {
	// Config holds the settings that are not resolved from several
	// sources: Script and GracefulStop. VUs and Duration are resolved by Run.
	Config config.Config
	// Overrides are the settings typed on the command line.
	Overrides config.Overrides
	// Getenv looks up environment variables for LoadTool's own settings
	// (LOADTOOL_VUS, ...); nil means none are set.
	Getenv func(string) (string, bool)
	// Env is what scripts see as __ENV. It must not be modified during
	// the run; nil means an empty __ENV.
	Env map[string]string
	// Console receives the scripts' console output; nil discards it.
	Console io.Writer
	// Warn receives non-fatal problems, such as unsupported script options.
	// nil discards them.
	Warn func(msg string)
}

// Run executes a test through its lifecycle (ADR-008):
//
//  1. load the script and run its top-level code once (VU 0)
//  2. read and resolve options
//  3. setup(), once
//  4. create the VUs and run the load phase
//  5. teardown(data), once
//
// It returns an error, and no result, when the test cannot start: script,
// options, setup or VU start-up problems. A failed setup ends the test
// there: no load phase and no teardown.
//
// Teardown runs whenever setup completed (or the script has none), even
// if VU start-up failed or the load phase was interrupted, so resources
// setup created are released. A teardown failure never hides the load
// phase's result: it is returned in Result.TeardownError, or joined to
// the start-up error.
//
// If ctx is cancelled during the load phase, the result is partial and
// marked Interrupted; that is not an error here, so the caller decides
// how to report it. Teardown then still runs, on a context that ignores
// that cancellation, bounded by teardownTimeout.
func Run(ctx context.Context, p Params) (report.Result, error) {
	cfg := p.Config
	if cfg.Script == "" {
		return report.Result{}, cfg.Validate()
	}
	prog, err := script.Load(cfg.Script)
	if err != nil {
		return report.Result{}, fmt.Errorf("load script: %w", err)
	}
	// Before reading options, so options can use __ENV too.
	prog = prog.WithEnv(p.Env).WithConsole(p.Console).WithWarn(p.Warn)

	// The lifecycle runtime runs the top-level code once, then options,
	// setup and teardown.
	lc, err := prog.NewLifecycle(ctx)
	if err != nil {
		return report.Result{}, err
	}
	raw, err := lc.Options()
	if err != nil {
		return report.Result{}, err
	}
	opts, unknown, err := config.ParseOptions(raw)
	if err != nil {
		return report.Result{}, err
	}
	for _, k := range unknown {
		if p.Warn != nil {
			p.Warn(fmt.Sprintf("script option %q is not supported yet and was ignored", k))
		}
	}
	if err := cfg.Resolve(p.Overrides, p.Getenv, opts); err != nil {
		return report.Result{}, err
	}
	if err := cfg.Validate(); err != nil {
		return report.Result{}, err
	}
	prog = prog.WithDiscardResponseBodies(cfg.DiscardResponseBodies)

	// One client for all VUs: http.Client is safe for concurrent use and a
	// shared transport lets each VU keep its own pooled connection.
	client := httpclient.New(cfg.VUs, httpclient.DefaultTimeout)
	defer client.CloseIdleConnections()

	data, err := lc.Setup(ctx, client, cfg.SetupTimeout)
	if err != nil {
		return report.Result{}, err
	}
	prog = prog.WithSetupData(data)

	newVU := func(i int) (engine.IterationFunc, error) {
		// ctx lets Ctrl+C interrupt a script's top-level code. VUs are
		// numbered from 1 in __VU, as in k6.
		vu, err := prog.NewVU(ctx, i+1, client)
		if err != nil {
			return nil, err
		}
		return vu.Iterate, nil
	}

	res, runErr := engine.Run(ctx, cfg.VUs, cfg.Duration, cfg.GracefulStop, newVU)
	// Read before teardown: a Ctrl+C during teardown does not make the
	// load phase interrupted.
	interrupted := ctx.Err() != nil

	teardownErr := lc.Teardown(teardownContext(ctx), client, cfg.TeardownTimeout, data)
	if runErr != nil {
		return report.Result{}, errors.Join(runErr, teardownErr)
	}
	result := report.Result{
		Script:       cfg.Script,
		VUs:          cfg.VUs,
		Duration:     cfg.Duration,
		GracefulStop: cfg.GracefulStop,
		Elapsed:      res.Elapsed,
		Interrupted:  interrupted,
		Summary:      res.Summary,
	}
	if teardownErr != nil {
		result.TeardownError = teardownErr.Error()
	}
	return result, nil
}

// teardownContext is the context teardown runs in. If ctx was cancelled
// during the load phase (the first Ctrl+C), teardown must still release
// what setup created, so it ignores that cancellation; teardownTimeout
// still bounds it, and a second Ctrl+C ends the process. Otherwise a
// Ctrl+C during teardown stops it.
func teardownContext(ctx context.Context) context.Context {
	if ctx.Err() != nil {
		return context.WithoutCancel(ctx)
	}
	return ctx
}
