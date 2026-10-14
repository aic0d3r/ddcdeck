// Package cmd implements the ddcdeck CLI. Every command is a thin client of the socket API;
// without a running daemon it drives an in-process deck.Service instead.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/automation"
	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
	"github.com/aic0d3r/ddcdeck/internal/platform/linux/ddcutil"
	"github.com/aic0d3r/ddcdeck/internal/platform/linux/diag"
	"github.com/aic0d3r/ddcdeck/internal/platform/linux/notify"
)

// Version is set at build time with -ldflags "-X github.com/aic0d3r/ddcdeck/cmd.Version=...".
var Version = "dev"

var (
	displayFlag string
	jsonFlag    bool
	quietFlag   bool
)

var rootCmd = &cobra.Command{
	Use:           "ddcdeck",
	Version:       Version,
	Short:         "Control monitor features over DDC/CI",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&displayFlag, "display", "d", "", "monitor: index, model, serial, connector or ID (default: first)")
	pf.BoolVar(&jsonFlag, "json", false, "print JSON")
	pf.BoolVarP(&quietFlag, "quiet", "q", false, "no output on success")
}

// Execute runs the CLI and returns the process exit code
// (0 ok, 1 usage, 2 no monitor, 3 unsupported, 4 DDC error).
func Execute(args []string) int {
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "ddcdeck:", err)
	var e *deck.Error
	if errors.As(err, &e) {
		switch e.Code {
		case deck.ErrNoMonitor:
			return 2
		case deck.ErrUnsupported:
			return 3
		case deck.ErrDDC:
			return 4
		}
	}
	return 1
}

type caller interface {
	Call(method string, params, out any) error
}

// local runs the API in-process, round-tripping through JSON so behaviour matches the socket.
type local struct{ s *deck.Service }

func (l local) Call(method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := l.s.Handle(method, raw)
	if err != nil {
		return err
	}
	if out == nil {
		return nil // caller only wanted success/failure, like Client.Call
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func newService() (*deck.Service, *automation.Engine, error) {
	models, err := model.Load()
	if err != nil {
		slog.Warn("some model files were skipped", "err", err)
	}
	s := deck.New(ddcutil.Backend{}, models)
	_, err = s.Scan()
	// automation methods are registered even without the daemon: the CLI fallback and the
	// daemon share them; the engine's scheduler only runs in the daemon
	automation.SetNotifier(notify.Send)
	automation.SetDiagnoser(func(s *deck.Service) any { return diag.Run(s) })
	eng := automation.NewEngine(s)
	automation.Register(s, eng)
	return s, eng, err
}

func connect() (caller, error) {
	if c, err := deck.Dial(deck.SocketPath()); err == nil {
		return c, nil
	}
	s, _, err := newService()
	if err != nil {
		return nil, err
	}
	return local{s}, nil
}

// call runs one API method against the daemon (or in-process) and decodes into out.
// params may be a deck.Params or any JSON-marshalable value.
func call(method string, params any, out any) error {
	c, err := connect()
	if err != nil {
		return err
	}
	if p, ok := params.(deck.Params); ok && p.Display == "" {
		p.Display = displayFlag
		params = p
	}
	return c.Call(method, params, out)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
