package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/engine"
	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/script"
)

func newRunCmd() *cobra.Command {
	cfg := config.Config{VUs: 1, Duration: 10 * time.Second}

	cmd := &cobra.Command{
		Use:     "run <script>",
		Short:   "Run a load test script",
		Example: "  loadtool run examples/basic.ts --vus 100 --duration 30s",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Script = args[0]
			if err := cfg.Validate(); err != nil {
				return err
			}
			return runTest(cmd, cfg)
		},
	}

	f := cmd.Flags()
	f.IntVarP(&cfg.VUs, "vus", "u", cfg.VUs, "number of concurrent virtual users")
	f.DurationVarP(&cfg.Duration, "duration", "d", cfg.Duration, "test duration, e.g. 30s or 5m")
	return cmd
}

func runTest(cmd *cobra.Command, cfg config.Config) error {
	prog, err := script.Load(cfg.Script)
	if err != nil {
		return fmt.Errorf("load script: %w", err)
	}

	// One client for all VUs: http.Client is safe for concurrent use and a
	// shared transport lets each VU keep its own pooled connection.
	client := httpclient.New(cfg.VUs, httpclient.DefaultTimeout)
	defer client.CloseIdleConnections()

	newVU := func(int) (engine.IterationFunc, error) {
		vu, err := prog.NewVU(client)
		if err != nil {
			return nil, err
		}
		return vu.Iterate, nil
	}

	ctx := cmd.Context()
	res, err := engine.Run(ctx, cfg.VUs, cfg.Duration, newVU)
	if err != nil {
		return err
	}
	interrupted := ctx.Err() != nil
	printSummary(cmd.OutOrStdout(), cfg, res, interrupted)
	if interrupted {
		// Partial results were printed; still fail so automation notices.
		return fmt.Errorf("test interrupted: %w", context.Cause(ctx))
	}
	return nil
}
