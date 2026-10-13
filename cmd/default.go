package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/deck"
)

var clearDefault bool

func init() {
	c := &cobra.Command{
		Use:   "default [selector]",
		Short: "Show or pin the monitor used when -d is omitted",
		Long: "Without a selector, shows the pinned monitor (default: the first one detected).\n" +
			"With one, pins it by index, model, serial, connector or ID, so commands and\n" +
			"rules keep addressing the same screen after a dock/undock renumbers buses.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: monitorCompletions,
		RunE: func(_ *cobra.Command, args []string) error {
			if clearDefault {
				if len(args) > 0 {
					return errors.New("give either a selector or --clear")
				}
				if err := deck.SetDefaultDisplay(""); err != nil {
					return err
				}
				return showDefault()
			}
			if len(args) == 0 {
				return showDefault()
			}
			var m deck.Monitor
			if err := call("monitor", deck.Params{Display: args[0]}, &m); err != nil {
				return err // refuses selectors that match nothing connected
			}
			if err := deck.SetDefaultDisplay(args[0]); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Printf("default monitor: %s (%s)\n", args[0], m.ID)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&clearDefault, "clear", false, "go back to the first detected monitor")
	rootCmd.AddCommand(c)
}

func showDefault() error {
	if jsonFlag {
		return printJSON(map[string]string{"default_display": deck.DefaultDisplay()})
	}
	def := deck.DefaultDisplay()
	if def == "" {
		fmt.Println("no default monitor: using the first one detected")
		return nil
	}
	var m deck.Monitor
	if err := call("monitor", deck.Params{Display: def}, &m); err != nil {
		fmt.Printf("default monitor: %s (%s)\n", def, err)
		return nil
	}
	fmt.Printf("default monitor: %s → %s (%s)\n", def, m.ID, m.Connector)
	return nil
}

// monitorCompletions offers the connected monitors for `ddcdeck default <TAB>`.
func monitorCompletions(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	var res deck.ListResult
	if err := call("list", nil, &res); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, m := range res.Monitors {
		out = append(out, m.Model, m.Connector, m.ID)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
