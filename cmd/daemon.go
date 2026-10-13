package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/appwatch"
	"github.com/aic0d3r/ddcdeck/internal/deck"
)

func init() {
	d := &cobra.Command{Use: "daemon", Short: "Run or inspect ddcdeckd"}
	d.AddCommand(&cobra.Command{
		Use: "run", Short: "Run the daemon in the foreground (what ddcdeckd does)", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return runDaemon() },
	})
	d.AddCommand(&cobra.Command{
		Use: "status", Short: "Check whether the daemon is running", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			c, err := deck.Dial(deck.SocketPath())
			if err != nil {
				return errors.New("daemon not running (CLI falls back to direct DDC)")
			}
			defer c.Close()
			var res deck.ListResult
			if err := c.Call("list", nil, &res); err != nil {
				return err
			}
			if jsonFlag {
				return printJSON(res)
			}
			fmt.Printf("running on %s, api v%d, %d monitor(s)\n", deck.SocketPath(), res.APIVersion, len(res.Monitors))
			return nil
		},
	})
	d.AddCommand(&cobra.Command{
		Use: "logs", Short: "Follow the daemon journal", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			j := exec.Command("journalctl", "--user", "-u", "ddcdeckd", "-n", "100", "-f")
			j.Stdout, j.Stderr = os.Stdout, os.Stderr
			return j.Run()
		},
	})
	rootCmd.AddCommand(d)
}

func runDaemon() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s, eng, err := newService()
	if err != nil {
		slog.Warn("initial scan failed, waiting for hotplug", "err", err) // keep running: a monitor may appear later
	}
	go eng.Run(ctx) // sun/time rules; hotplug rules ride the watcher below
	if w, err := appwatch.Start(ctx, eng.OnApp); err != nil {
		slog.Info("app watcher not started", "err", err)
	} else if w.Active() {
		defer w.Stop()
	}
	var list deck.ListResult
	if res, err := s.Handle("list", nil); err == nil {
		list = res.(deck.ListResult)
	}
	for _, m := range list.Monitors {
		slog.Info("monitor", "id", m.ID, "connector", m.Connector, "bus", m.Bus, "model_file", m.HasModel)
	}
	go func() {
		if err := s.WatchHotplug(ctx); err != nil {
			slog.Error("hotplug watcher stopped", "err", err)
		}
	}()
	errc := make(chan error, 1)
	go func() { errc <- s.Serve(ctx, deck.SocketPath()) }()
	deck.Notify("READY=1")
	slog.Info("listening", "socket", deck.SocketPath())
	err = <-errc
	_ = os.Remove(deck.SocketPath())
	return err
}
