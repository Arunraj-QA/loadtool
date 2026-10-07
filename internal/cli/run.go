package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/runner"
	"github.com/Arunraj-QA/loadtool/internal/thresholds"
)

func newRunCmd() *cobra.Command {
	cfg := config.Config{GracefulStop: 30 * time.Second}
	// vus and duration are separate from cfg: they only override the
	// script's options and the environment when typed (ADR-006).
	var (
		vus         int
		duration    time.Duration
		envFlags    []string
		outFlags    []string
		summaryJSON string
		reportHTML  string
	)

	cmd := &cobra.Command{
		Use:     "run <script>",
		Short:   "Run a load test script",
		Example: "  loadtool run examples/basic-http.ts --vus 10 --duration 10s",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Script = args[0]
			var cli config.Overrides
			if cmd.Flags().Changed("vus") {
				cli.VUs = &vus
			}
			if cmd.Flags().Changed("duration") {
				cli.Duration = &duration
			}
			env, err := scriptEnv(os.Environ(), envFlags)
			if err != nil {
				return err
			}
			out, err := parseOutputs(outFlags)
			if err != nil {
				return err
			}
			if summaryJSON != "" {
				out.jsonFiles = append(out.jsonFiles, jsonFile{flag: "--summary-json", path: summaryJSON})
			}
			out.reportHTML = reportHTML
			return runTest(cmd, cfg, cli, env, out)
		},
	}

	f := cmd.Flags()
	f.IntVarP(&vus, "vus", "u", config.DefaultVUs,
		"number of concurrent virtual users (overrides "+config.EnvVUs+" and options.vus)")
	f.DurationVarP(&duration, "duration", "d", config.DefaultDuration,
		"test duration, e.g. 30s or 5m (overrides "+config.EnvDuration+" and options.duration)")
	f.DurationVar(&cfg.GracefulStop, "graceful-stop", cfg.GracefulStop,
		"how long iterations still running at the end of --duration may take to finish (0 cancels them at once)")
	f.StringArrayVarP(&outFlags, "out", "o", nil,
		"write the summary as JSON: \"json\" to stdout (the console summary then goes to stderr) "+
			"or \"json=<file>\" to a file (repeatable; docs/json-summary.md)")
	f.StringVar(&summaryJSON, "summary-json", "",
		"same as --out json=<file>")
	f.StringVar(&reportHTML, "report-html", "",
		"also write a self-contained HTML report with charts to this file")
	f.StringArrayVarP(&envFlags, "env", "e", nil,
		"set a variable for the script's __ENV, as KEY=VALUE (repeatable; overrides the process environment)")
	return cmd
}

// scriptEnv builds the script's __ENV: the process environment (environ,
// as from os.Environ) with --env KEY=VALUE flags applied on top.
func scriptEnv(environ, flags []string) (map[string]string, error) {
	env := make(map[string]string, len(environ)+len(flags))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			env[k] = v
		}
	}
	for _, kv := range flags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--env %q must have the form KEY=VALUE", kv)
		}
		env[k] = v
	}
	return env, nil
}

// runTest runs the test through the runner, prints the console report and
// turns an interrupted run into an error (exit code 1).
// outputs are the optional result outputs; empty means not written.
type outputs struct {
	jsonStdout bool       // --out json
	jsonFiles  []jsonFile // --out json=<file>, --summary-json
	reportHTML string
}

// jsonFile is a JSON summary file and the flag that asked for it, for
// error messages.
type jsonFile struct{ flag, path string }

// parseOutputs reads --out values: "json" (stdout) or "json=<file>".
func parseOutputs(flags []string) (outputs, error) {
	var out outputs
	for _, f := range flags {
		kind, path, hasPath := strings.Cut(f, "=")
		switch {
		case kind == "json" && !hasPath:
			out.jsonStdout = true
		case kind == "json" && path != "":
			out.jsonFiles = append(out.jsonFiles, jsonFile{flag: "--out " + f, path: path})
		default:
			return outputs{}, fmt.Errorf("--out %q: supported outputs are json (to stdout) and json=<file>", f)
		}
	}
	return out, nil
}

func runTest(cmd *cobra.Command, cfg config.Config, cli config.Overrides, env map[string]string, out outputs) error {
	ctx := cmd.Context()
	res, err := runner.Run(ctx, runner.Params{
		Config:    cfg,
		Overrides: cli,
		Getenv:    os.LookupEnv,
		Env:       env,
		Console:   cmd.ErrOrStderr(),
		Warn: func(msg string) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", msg)
		},
	})
	if err != nil {
		return err
	}
	// The results are printed first, so a failed teardown or an interrupt
	// never hides them; either still fails the run so automation notices.
	// With JSON on stdout, the console summary moves to stderr so stdout
	// stays valid JSON.
	consoleOut := cmd.OutOrStdout()
	if out.jsonStdout {
		consoleOut = cmd.ErrOrStderr()
	}
	report.Console(consoleOut, res)
	var errs []error
	// Written whenever there is a result, also for interrupted runs and
	// failed thresholds; a write failure exits 1 after the summary.
	if out.jsonStdout {
		if err := report.JSON(cmd.OutOrStdout(), res, Version); err != nil {
			errs = append(errs, fmt.Errorf("--out json: %w", err))
		}
	}
	for _, f := range out.jsonFiles {
		if err := report.WriteJSONFile(f.path, res, Version); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.flag, err))
		}
	}
	if out.reportHTML != "" {
		if err := report.WriteHTMLFile(out.reportHTML, res, Version); err != nil {
			errs = append(errs, fmt.Errorf("--report-html: %w", err))
		}
	}
	// report.Verdict decides the exit code, as it decides the JSON
	// summary's outcome, so the two always agree.
	if v := report.Verdict(res); v.ExitCode == report.ExitFailed {
		if res.Interrupted {
			errs = append(errs, fmt.Errorf("test interrupted: %w", context.Cause(ctx)))
		}
		if res.TeardownError != "" {
			errs = append(errs, errors.New(res.TeardownError))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...) // exit 1, even if thresholds also failed
	}
	return thresholdsError(res.Thresholds)
}

// thresholdsError returns an error with exit code 99 naming the failed
// thresholds, or nil if all passed.
func thresholdsError(rs []thresholds.Result) error {
	if !thresholds.Failed(rs) {
		return nil
	}
	var failed []string
	for _, r := range rs {
		if !r.Passed {
			failed = append(failed, r.Metric+" "+r.Expr)
		}
	}
	return &ExitError{
		Code: ExitThresholdsFailed,
		Err:  fmt.Errorf("thresholds failed: %s", strings.Join(failed, ", ")),
	}
}
