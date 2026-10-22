package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Arunraj-QA/loadtool/internal/ui"
)

func newUICmd() *cobra.Command {
	var (
		port         int
		lim          ui.Limits
		allowPrivate bool
	)
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Load-test one HTTP API from a local web page, without a script",
		Long: "Serves a page on this machine (127.0.0.1 only) where you enter one HTTP API,\n" +
			"configure VUs and duration, run the test with LoadTool's engine and see the results.\n\n" +
			"Only public internet addresses are allowed unless --allow-private-targets is set.\n" +
			"Only test APIs you are authorized to load-test.",
		Example: "  loadtool ui\n  loadtool ui --port 7878 --max-vus 100",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lim.AllowPrivate = allowPrivate
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				return err
			}
			p := ln.Addr().(*net.TCPAddr).Port
			stderr := cmd.ErrOrStderr()
			ctl := ui.NewController(ui.Options{Limits: lim, Version: Version, Log: stderr})
			token := ui.NewToken()
			srv := &http.Server{
				Handler:           ui.NewServer(ctl, token, fmt.Sprintf("127.0.0.1:%d", p), fmt.Sprintf("localhost:%d", p)),
				ReadHeaderTimeout: 10 * time.Second,
			}
			if allowPrivate {
				fmt.Fprintln(stderr, "warning: --allow-private-targets: tests may reach this machine and private networks")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "LoadTool UI: http://127.0.0.1:%d/#token=%s\n(Ctrl+C to stop)\n", p, token)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			served := make(chan error, 1)
			go func() { served <- srv.Serve(ln) }()
			select {
			case err := <-served:
				return err
			case <-ctx.Done():
			}
			ctl.Close(lim.RequestTimeout + 5*time.Second)
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdown); err != nil {
				return err
			}
			if err := <-served; !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&port, "port", 0, "port on 127.0.0.1 (0 picks a free one)")
	f.IntVar(&lim.MaxVUs, "max-vus", 50, "the most VUs a test may use")
	f.DurationVar(&lim.MaxDuration, "max-duration", 5*time.Minute, "the longest a test may run")
	f.DurationVar(&lim.RequestTimeout, "request-timeout", 10*time.Second, "timeout of each request")
	f.BoolVar(&allowPrivate, "allow-private-targets", false,
		"allow loopback and private-network destinations (for local servers such as the demo API)")
	return cmd
}
