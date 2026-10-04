// Package runner orchestrates one test run: it loads the script, resolves
// the run settings, starts the VUs, runs the engine and returns the result
// for the reporters. It knows nothing about the command line; the CLI (or
// any other caller) supplies the inputs and renders the result.
package runner

import (
	"context"
	"fmt"

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
	// Getenv looks up environment variables; nil means none are set.
	Getenv func(string) (string, bool)
	// Warn receives non-fatal problems, such as unsupported script options.
	// nil discards them.
	Warn func(msg string)
}

// Run executes a test. It returns an error, and no result, when the run
// cannot start (script, options or VU start-up problems). Once load has
// started it always returns a result. If ctx is cancelled during the run,
// the result is partial and marked Interrupted; that is not an error here,
// so the caller decides how to report it.
func Run(ctx context.Context, p Params) (report.Result, error) {
	cfg := p.Config
	if cfg.Script == "" {
		return report.Result{}, cfg.Validate()
	}
	prog, err := script.Load(cfg.Script)
	if err != nil {
		return report.Result{}, fmt.Errorf("load script: %w", err)
	}

	raw, err := prog.Options(ctx)
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

	// One client for all VUs: http.Client is safe for concurrent use and a
	// shared transport lets each VU keep its own pooled connection.
	client := httpclient.New(cfg.VUs, httpclient.DefaultTimeout)
	defer client.CloseIdleConnections()

	newVU := func(int) (engine.IterationFunc, error) {
		// ctx lets Ctrl+C interrupt a script's top-level code.
		vu, err := prog.NewVU(ctx, client)
		if err != nil {
			return nil, err
		}
		return vu.Iterate, nil
	}

	res, err := engine.Run(ctx, cfg.VUs, cfg.Duration, cfg.GracefulStop, newVU)
	if err != nil {
		return report.Result{}, err
	}
	return report.Result{
		Script:       cfg.Script,
		VUs:          cfg.VUs,
		Duration:     cfg.Duration,
		GracefulStop: cfg.GracefulStop,
		Elapsed:      res.Elapsed,
		Interrupted:  ctx.Err() != nil,
		Summary:      res.Summary,
	}, nil
}
