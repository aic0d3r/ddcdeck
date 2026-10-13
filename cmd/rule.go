package cmd

import (
	"fmt"
	"strconv"

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
		Use:   "location [lat lon]",
		Short: "Show or set the location sun rules use",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 && len(args) != 2 {
				return fmt.Errorf("give no arguments to show, or both lat and lon")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, a []string) error {
			if len(a) == 0 {
				lat, lon := location.Default()
				var res struct {
					Location Location `json:"location"`
				}
				if err := call("rules.list", map[string]any{}, &res); err == nil {
					lat, lon = res.Location.Lat, res.Location.Lon
				}
				if jsonFlag {
					return printJSON(map[string]float64{"lat": lat, "lon": lon})
				}
				fmt.Printf("%.2f, %.2f\n", lat, lon)
				return nil
			}
			lat, err := strconv.ParseFloat(a[0], 64)
			if err == nil {
				_, err = strconv.ParseFloat(a[1], 64)
			}
			if err != nil || lat < -90 || lat > 90 {
				return fmt.Errorf("expected two numbers: lat (-90..90) lon (-180..180)")
			}
			var res struct {
				Location Location `json:"location"`
			}
			lon, _ := strconv.ParseFloat(a[1], 64)
			if err := call("location.set", map[string]any{"lat": lat, "lon": lon}, &res); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Printf("location set to %.2f, %.2f\n", res.Location.Lat, res.Location.Lon)
			}
			return nil
		},
	})
	rootCmd.AddCommand(rule)
}
