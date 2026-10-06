// Command loadtool is the LoadTool CLI entrypoint.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/Arunraj-QA/loadtool/internal/cli"
)

func main() {
	// Ctrl+C cancels the run; VUs stop and a partial summary is printed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// After the first Ctrl+C, restore the default handling so a second one
	// terminates the process even if something is not responding to ctx.
	context.AfterFunc(ctx, stop)

	if err := cli.NewRootCmd(os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		stop()
		// 1 for errors, 99 when only thresholds failed (cli.ExitCode).
		os.Exit(cli.ExitCode(err))
	}
}
