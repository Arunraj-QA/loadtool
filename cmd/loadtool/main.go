// Command loadtool is the LoadTool CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/Arunraj-QA/loadtool/internal/cli"
)

func main() {
	if err := cli.NewRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
