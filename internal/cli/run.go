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
	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

func newRunCmd() *cobra.Command {
	cfg := config.Config{VUs: 1, Duration: 10 * time.Second}

	cmd := &cobra.Command{
		Use:     "run <script>",
		Short:   "Run a load test script",
		Example: "  loadtool run test.ts --url http://localhost:8080/ --vus 100 --duration 30s",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Script = args[0]
			if err := cfg.Validate(); err != nil {
				return err
			}
			if _, err := os.Stat(cfg.Script); err != nil {
				return fmt.Errorf("script: %w", err)
			}
			return runTest(cmd, cfg)
		},
	}

	f := cmd.Flags()
	f.StringVar(&cfg.URL, "url", "", "target URL requested by each iteration (required)")
	f.IntVarP(&cfg.VUs, "vus", "u", cfg.VUs, "number of concurrent virtual users")
	f.DurationVarP(&cfg.Duration, "duration", "d", cfg.Duration, "test duration, e.g. 30s or 5m")
	return cmd
}

func runTest(cmd *cobra.Command, cfg config.Config) error {
	// Script execution arrives with the goja runtime; until then the
	// iteration is a fixed GET so the engine can be exercised end to end.
	fmt.Fprintf(cmd.ErrOrStderr(),
		"note: script execution is not implemented yet; each iteration sends GET %s\n", cfg.URL)

	client := httpclient.New(cfg.VUs, httpclient.DefaultTimeout)
	defer client.CloseIdleConnections()

	iter := func(ctx context.Context, rec *metrics.Recorder) {
		httpclient.Get(ctx, client, cfg.URL, rec)
	}

	ctx := cmd.Context()
	res := engine.Run(ctx, cfg.VUs, cfg.Duration, iter)
	interrupted := ctx.Err() != nil
	printSummary(cmd.OutOrStdout(), cfg, res, interrupted)
	if interrupted {
		// Partial results were printed; still fail so automation notices.
		return fmt.Errorf("test interrupted: %w", context.Cause(ctx))
	}
	return nil
}
