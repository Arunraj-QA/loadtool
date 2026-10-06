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
)

func newRunCmd() *cobra.Command {
	cfg := config.Config{GracefulStop: 30 * time.Second}
	// vus and duration are separate from cfg: they only override the
	// script's options and the environment when typed (ADR-006).
	var (
		vus      int
		duration time.Duration
		envFlags []string
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
			return runTest(cmd, cfg, cli, env)
		},
	}

	f := cmd.Flags()
	f.IntVarP(&vus, "vus", "u", config.DefaultVUs,
		"number of concurrent virtual users (overrides "+config.EnvVUs+" and options.vus)")
	f.DurationVarP(&duration, "duration", "d", config.DefaultDuration,
		"test duration, e.g. 30s or 5m (overrides "+config.EnvDuration+" and options.duration)")
	f.DurationVar(&cfg.GracefulStop, "graceful-stop", cfg.GracefulStop,
		"how long iterations still running at the end of --duration may take to finish (0 cancels them at once)")
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
func runTest(cmd *cobra.Command, cfg config.Config, cli config.Overrides, env map[string]string) error {
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
	report.Console(cmd.OutOrStdout(), res)
	var errs []error
	if res.Interrupted {
		errs = append(errs, fmt.Errorf("test interrupted: %w", context.Cause(ctx)))
	}
	if res.TeardownError != "" {
		errs = append(errs, errors.New(res.TeardownError))
	}
	return errors.Join(errs...)
}
