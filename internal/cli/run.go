package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// ErrNotImplemented is returned by commands that exist but are not yet built.
// It produces a non-zero exit code so automation never mistakes a skipped
// load test for a passing one.
var ErrNotImplemented = errors.New("not implemented yet")

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <script>",
		Short: "Run a load test script",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("run %q: %w", args[0], ErrNotImplemented)
		},
	}
}
