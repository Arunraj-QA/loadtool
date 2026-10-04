package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/engine"
	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/script"
)

func newRunCmd() *cobra.Command {
	cfg := config.Config{GracefulStop: 30 * time.Second}
	// vus and duration are separate from cfg: they only override the
	// script's options and the environment when typed (ADR-006).
	var (
		vus      int
		duration time.Duration
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
			return runTest(cmd, cfg, cli)
		},
	}

	f := cmd.Flags()
	f.IntVarP(&vus, "vus", "u", config.DefaultVUs,
		"number of concurrent virtual users (overrides "+config.EnvVUs+" and options.vus)")
	f.DurationVarP(&duration, "duration", "d", config.DefaultDuration,
		"test duration, e.g. 30s or 5m (overrides "+config.EnvDuration+" and options.duration)")
	f.DurationVar(&cfg.GracefulStop, "graceful-stop", cfg.GracefulStop,
		"how long iterations still running at the end of --duration may take to finish (0 cancels them at once)")
	return cmd
}

func runTest(cmd *cobra.Command, cfg config.Config, cli config.Overrides) error {
	if cfg.Script == "" {
		return cfg.Validate()
	}
	prog, err := script.Load(cfg.Script)
	if err != nil {
		return fmt.Errorf("load script: %w", err)
	}

	ctx := cmd.Context()
	raw, err := prog.Options(ctx)
	if err != nil {
		return err
	}
	opts, unknown, err := config.ParseOptions(raw)
	if err != nil {
		return err
	}
	for _, k := range unknown {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: script option %q is not supported yet and was ignored\n", k)
	}
	if err := cfg.Resolve(cli, os.LookupEnv, opts); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
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
		return err
	}
	interrupted := ctx.Err() != nil
	report.Console(cmd.OutOrStdout(), report.Result{
		Script:       cfg.Script,
		VUs:          cfg.VUs,
		Duration:     cfg.Duration,
		GracefulStop: cfg.GracefulStop,
		Elapsed:      res.Elapsed,
		Interrupted:  interrupted,
		Summary:      res.Summary,
	})
	if interrupted {
		// Partial results were printed; still fail so automation notices.
		return fmt.Errorf("test interrupted: %w", context.Cause(ctx))
	}
	return nil
}
