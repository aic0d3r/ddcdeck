package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/location"
)

// rule commands: the GUI's rule editor, for terminals. `rules` stays the listing.
func init() {
	rule := &cobra.Command{Use: "rule", Short: "Add or remove automation rules"}
	rule.AddCommand(&cobra.Command{
		Use:   "add <when> <profile> [profile...]",
		Short: "Add a rule (sunset, sunrise, HH:MM, connected, disconnected)",
		Long: "Same as the GUI's Add… button. App rules need a class list, so they stay\n" +
			"in rules.toml.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			var res map[string]any
			if err := call("rule.add", map[string]any{"when": a[0], "apply": a[1:]}, &res); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Println("added:", res["added"])
			}
			return nil
		},
	})
	rule.AddCommand(&cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a rule by its id (shown by `ddcdeck rules`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			if err := call("rule.rm", map[string]any{"id": a[0]}, nil); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Println("removed:", a[0])
			}
			return nil
		},
	})
	rule.AddCommand(&cobra.Command{
		Use:   "location [city | \"lat lon\"]",
		Short: "Show or set the location sun rules use",
		Long: "A city is resolved through the system's tzdata (the same source the\n" +
			"timezone default uses): ddcdeck rule location Tokyo",
		Args: cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			if len(a) == 0 {
				lat, lon := location.Default()
				var list struct {
					Location Location `json:"location"`
				}
				if err := call("rules.list", map[string]any{}, &list); err == nil {
					lat, lon = list.Location.Lat, list.Location.Lon
				}
				if jsonFlag {
					return printJSON(map[string]float64{"lat": lat, "lon": lon})
				}
				name := location.Name(lat, lon)
				if name != "" {
					name = " (" + name + ")"
				}
				fmt.Printf("%.2f, %.2f%s\n", lat, lon, name)
				return nil
			}
			var res struct {
				Location Location `json:"location"`
				Name     string   `json:"name"`
			}
			if err := call("location.set", map[string]any{"query": strings.Join(a, " ")}, &res); err != nil {
				return err
			}
			if !quietFlag {
				msg := fmt.Sprintf("location set to %.2f, %.2f", res.Location.Lat, res.Location.Lon)
				if res.Name != "" {
					msg += " — " + res.Name
				}
				fmt.Println(msg)
			}
			return nil
		},
	})
	rootCmd.AddCommand(rule)
}
