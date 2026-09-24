// Package cli wires the loadtool command-line interface.
//
// It owns argument parsing and user-facing output only; engine logic must
// live in separate packages so it can be tested without the CLI.
package cli

import (
	"io"

	"github.com/spf13/cobra"
)

// Version is the loadtool release version. It is overridden at build time:
//
//	go build -ldflags "-X github.com/Arunraj-QA/loadtool/internal/cli.Version=v0.1.0"
var Version = "dev"

// NewRootCmd builds the loadtool root command. Output streams are injected
// so tests can capture them and no package-level command state exists.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "loadtool",
		Short: "LoadTool is an API performance and load-testing engine",
		Long: "LoadTool runs scripted load tests against HTTP APIs.\n\n" +
			"Tests are written in TypeScript and executed by a Go engine.",
		Version: Version,
		// Errors are reported once by the caller; usage is only useful for
		// argument errors, which Cobra already explains.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	root.AddCommand(newRunCmd())
	return root
}
