// Package runner orchestrates one test run: it loads the script, resolves
// the run settings, starts the VUs, runs the engine and returns the result
// for the reporters. It knows nothing about the command line; the CLI (or
// any other caller) supplies the inputs and renders the result.
package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/engine"
	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/script"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
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
	// TLSConfig, if set, replaces the HTTP client's TLS settings. It is not
	// a user option: tests use it to trust a local test server's
	// certificate.
	TLSConfig *tls.Config
}

// Run executes a test through its lifecycle (ADR-008):
//
//  1. load the script and run its top-level code once (VU 0)
//  2. read and resolve options
//  3. setup(), once
//  4. create the VUs and run the load phase
//  5. teardown(data), once
//  6. evaluate thresholds against the load phase's metrics
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
	prog, err := script.Load(cfg.Script, modules...)
	if err != nil {
		return report.Result{}, fmt.Errorf("load script: %w", err)
	}
	// Before reading options, so options can use __ENV too.
	prog = prog.WithEnv(p.Env).WithConsole(p.Console).WithWarn(p.Warn)
	// Protocol modules the script imports start before its top-level code
	// runs (it may call them, as client.load does), and before thresholds
	// are parsed, so thresholds can name their metric families (ADR-015).
	families, err := prog.StartModules(protocol.RunEnv{TLS: p.TLSConfig})
	if err != nil {
		return report.Result{}, err
	}
	defer closeAndWarn(p.Warn, "protocol modules", prog.CloseModules)

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
	// Parsed now, so a typo fails before setup or any load.
	ths, err := thresholds.Parse(opts.Thresholds, families...)
	if err != nil {
		return report.Result{}, err
	}
	prog = prog.WithDiscardResponseBodies(cfg.DiscardResponseBodies).WithKeepCookies(cfg.NoCookiesReset)
	if prog, err = prepareExecs(prog, lc, cfg.Scenarios); err != nil {
		return report.Result{}, err
	}
	if cfg.ScenariosReplaced && p.Warn != nil {
		p.Warn("--vus/--duration (or LOADTOOL_VUS/LOADTOOL_DURATION) replace the script's scenarios or stages with one constant-vus scenario")
	}

	// One transport for all VUs, so they share one connection pool; each
	// VU wraps it in a client with its own cookie jar (ADR-009).
	client := httpclient.NewWithOptions(httpclient.Options{
		MaxConnsPerHost:   cfg.VUs,
		Timeout:           httpclient.DefaultTimeout,
		NoConnectionReuse: cfg.NoConnectionReuse,
		HTTPVersion:       cfg.HTTPVersion,
		TLSConfig:         p.TLSConfig,
	})
	defer client.CloseIdleConnections()

	// The lifecycle runtime's module instances close after teardown
	// (deferred calls run last-in first-out, so before the modules).
	defer closeAndWarn(p.Warn, "setup/teardown", lc.Close)
	data, err := lc.Setup(ctx, client, cfg.SetupTimeout)
	if err != nil {
		return report.Result{}, err
	}
	prog = prog.WithSetupData(data)

	var vus []*script.VU
	res, runErr := engine.RunScenarios(ctx, engineScenarios(ctx, prog, client, cfg.Scenarios, &vus))
	// Read before teardown: a Ctrl+C during teardown does not make the
	// load phase interrupted.
	interrupted := ctx.Err() != nil
	// Every VU goroutine has ended: close the VUs' module instances.
	for _, vu := range vus {
		closeAndWarn(p.Warn, fmt.Sprintf("VU %d", vu.ID()), vu.Close)
	}
	res.Summary.Families = prog.ModuleFamilies()

	teardownErr := lc.Teardown(teardownContext(ctx), client, cfg.TeardownTimeout, data)
	if runErr != nil {
		return report.Result{}, errors.Join(runErr, teardownErr)
	}
	result := report.Result{
		Script:       cfg.Script,
		VUs:          cfg.VUs,
		Duration:     cfg.Duration,
		GracefulStop: cfg.GracefulStop,
		Started:      res.Started,
		Series:       res.Series,
		Elapsed:      res.Elapsed,
		Interrupted:  interrupted,
		Summary:      res.Summary,
		Thresholds:   thresholds.Evaluate(ths, res.Summary, res.Elapsed),
	}
	result.Scenarios = cfg.Scenarios
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

// prepareExecs makes sure every function the scenarios run exists, before
// setup or any load: the default export through the lifecycle runtime,
// named exec functions through WithExecs.
func prepareExecs(prog *script.Program, lc *script.Lifecycle, scenarios []config.Scenario) (*script.Program, error) {
	var named []string
	for _, s := range scenarios {
		if s.Exec == "default" {
			if !lc.HasDefault() {
				return nil, fmt.Errorf("scenario %q runs the default function: %w", s.Name, script.ErrNoDefaultExport)
			}
			continue
		}
		named = append(named, s.Exec)
	}
	return prog.WithExecs(named)
}

// engineScenarios turns the resolved scenarios into engine scenarios. VUs
// are numbered from 1 across all scenarios, in order, as __VU.
// engineScenarios builds the engine's scenarios. Every VU created is
// appended to vus (VUs are created one at a time, before the clock
// starts), so the runner can close them after the run.
func engineScenarios(ctx context.Context, prog *script.Program, client *http.Client, scenarios []config.Scenario, vus *[]*script.VU) []engine.Scenario {
	out := make([]engine.Scenario, len(scenarios))
	firstVU := 1
	for i, s := range scenarios {
		base, exec := firstVU, s.Exec
		firstVU += s.MaxVUs()
		out[i] = engine.Scenario{
			Name:         s.Name,
			Executor:     executor(s),
			StartTime:    s.StartTime,
			GracefulStop: s.GracefulStop,
			NewVU: func(id int) (engine.IterationFunc, error) {
				// ctx lets Ctrl+C interrupt a script's top-level code.
				vu, err := prog.NewVUExec(ctx, base+id, exec, client)
				if err != nil {
					return nil, err
				}
				*vus = append(*vus, vu)
				return vu.Iterate, nil
			},
		}
	}
	return out
}

func executor(s config.Scenario) engine.Executor {
	switch s.Executor {
	case config.RampingVUs:
		stages := make([]engine.Stage, len(s.Stages))
		for i, st := range s.Stages {
			stages[i] = engine.Stage{Duration: st.Duration, Target: st.Target}
		}
		return engine.RampingVUs{StartVUs: s.StartVUs, Stages: stages, GracefulRampDown: s.GracefulRampDown}
	case config.ConstantArrivalRate:
		return engine.ConstantArrivalRate{Rate: s.Rate, TimeUnit: s.TimeUnit, Duration: s.Duration, PreAllocatedVUs: s.PreAllocatedVUs}
	default:
		return engine.ConstantVUs{VUs: s.VUs, Duration: s.Duration}
	}
}

// closeTimeout bounds each close step after a run (ADR-018 §4).
const closeTimeout = 5 * time.Second

// closeAndWarn runs close with a deadline; an error is a warning, not a
// failed run.
func closeAndWarn(warn func(string), what string, close func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if err := close(ctx); err != nil && warn != nil {
		warn(fmt.Sprintf("closing %s: %v", what, err))
	}
}
