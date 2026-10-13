package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/deck"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List monitors and their supported features", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var res deck.ListResult
			if err := call("list", deck.Params{}, &res); err != nil {
				return err
			}
			if jsonFlag {
				return printJSON(res)
			}
			if len(res.Monitors) == 0 {
				return &deck.Error{Code: deck.ErrNoMonitor, Message: "no DDC monitor found"}
			}
			for _, m := range res.Monitors {
				var fs []string
				for name := range m.Features {
					fs = append(fs, name)
				}
				slices.Sort(fs)
				note := ""
				if !m.HasModel {
					note = "  (no model file: standard features only)"
				}
				fmt.Printf("%d  %s  %s  bus %d%s\n   %s\n", m.Index, m.ID, m.Connector, m.Bus, note, strings.Join(fs, ", "))
			}
			return nil
		},
	})
	rootCmd.AddCommand(&cobra.Command{
		Use: "status", Short: "Show all supported features with current values", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var res deck.StatusResult
			if err := call("status", deck.Params{}, &res); err != nil {
				return err
			}
			if jsonFlag {
				return printJSON(res)
			}
			fmt.Println(res.Display)
			for _, v := range res.Values {
				fmt.Printf("  %-11s %s\n", v.Feature, formatValue(v))
			}
			return nil
		},
	})
}
