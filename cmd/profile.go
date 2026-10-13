package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/automation"
	"github.com/aic0d3r/ddcdeck/internal/deck"
)

func init() {
	profile := &cobra.Command{Use: "profile", Short: "Save and apply partial monitor states"}
	profile.AddCommand(&cobra.Command{
		Use: "list", Short: "List profiles", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var res struct {
				Profiles []automation.Profile `json:"profiles"`
			}
			if err := call("profile.list", map[string]any{}, &res); err != nil {
				return err
			}
			if jsonFlag {
				return printJSON(res.Profiles)
			}
			for _, p := range res.Profiles {
				var parts []string
				for _, s := range p.Monitors {
					parts = append(parts, s.Match+": "+strings.Join(fieldNames(s), " "))
				}
				fmt.Printf("%-12s %s\n", p.Name, strings.Join(parts, "; "))
			}
			return nil
		},
	})
	profile.AddCommand(&cobra.Command{
		Use: "show <name>", Short: "Print a profile", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			var p automation.Profile
			if err := call("profile.show", map[string]any{"name": a[0]}, &p); err != nil {
				return err
			}
			if jsonFlag {
				return printJSON(p)
			}
			fmt.Println(p.Name)
			for _, s := range p.Monitors {
				fmt.Printf("  [%s]\n", s.Match)
				for _, f := range fieldNames(s) {
					fmt.Printf("    %-12s %v\n", f, s.Fields[f])
				}
				for _, c := range s.OnApply {
					fmt.Printf("    on_apply     %s\n", c)
				}
			}
			return nil
		},
	})
	profile.AddCommand(&cobra.Command{
		Use: "apply <name>...", Short: "Apply profiles in order to every matching monitor", Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			var res struct {
				Results []struct {
					Display string   `json:"display"`
					Profile string   `json:"profile"`
					Fields  []string `json:"applied"`
					Skipped []string `json:"skipped"`
					Error   string   `json:"error"`
					Hooks   []string `json:"hooks"`
				} `json:"results"`
			}
			if err := call("profile.apply", map[string]any{"profiles": a}, &res); err != nil {
				return err
			}
			failed := false
			for _, r := range res.Results {
				line := r.Display + " ← " + r.Profile
				if len(r.Fields) > 0 && !quietFlag {
					line += " (" + strings.Join(r.Fields, " ") + ")"
				}
				fmt.Println(line)
				if len(r.Skipped) > 0 && !quietFlag {
					fmt.Println("  skipped (unsupported):", strings.Join(r.Skipped, " "))
				}
				if r.Error != "" {
					fmt.Fprintln(os.Stderr, "ddcdeck:", r.Error)
					failed = true
				}
			}
			if failed {
				return &deck.Error{Code: deck.ErrDDC, Message: "some profiles failed to apply"}
			}
			return nil
		},
	})
	save := &cobra.Command{
		Use: "save <name>", Short: "Save the selected monitor's current state as a profile", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			var p automation.Profile
			params := map[string]any{"name": a[0]}
			if displayFlag != "" {
				params["display"] = displayFlag
			}
			if len(saveFields) > 0 {
				params["fields"] = saveFields
			}
			if err := call("profile.save", params, &p); err != nil {
				return err
			}
			if !quietFlag {
				fmt.Println("saved", p.Name)
			}
			return nil
		},
	}
	save.Flags().StringSliceVar(&saveFields, "fields", nil, "comma-separated feature names (default: all supported)")
	profile.AddCommand(save)
	profile.AddCommand(&cobra.Command{
		Use: "edit <name>", Short: "Edit a profile in $EDITOR", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			path := filepath.Join(automation.ProfilesDir(), a[0]+".toml")
			if _, err := os.Stat(path); err != nil {
				if err := automation.SaveProfile(automation.Profile{Name: a[0]}); err != nil {
					return err
				}
			}
			return editFile(path)
		},
	})
	profile.AddCommand(&cobra.Command{
		Use: "rm <name>", Short: "Delete a profile", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return call("profile.rm", map[string]any{"name": a[0]}, nil)
		},
	})
	rootCmd.AddCommand(profile)

	auto := &cobra.Command{Use: "auto", Short: "Pause or resume all automation triggers"}
	auto.AddCommand(&cobra.Command{
		Use: "status", Short: "Show whether automation is running", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return autoStatus() },
	})
	auto.AddCommand(&cobra.Command{
		Use: "enable", Short: "Resume automation", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return autoSet(true) },
	})
	auto.AddCommand(&cobra.Command{
		Use: "disable", Short: "Pause automation (manual changes keep working)", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return autoSet(false) },
	})
	rootCmd.AddCommand(auto)

	rootCmd.AddCommand(&cobra.Command{
		Use: "rules", Short: "List automation rules and shortcuts", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var res rulesResult
			if err := call("rules.list", map[string]any{}, &res); err != nil {
				return err
			}
			return autoStatus()
		},
	})
}

var saveFields []string

type rulesResult struct {
	Auto        bool       `json:"auto"`
	Location    Location   `json:"location"`
	Rules       []Rule     `json:"rules"`
	Shortcuts   []Shortcut `json:"shortcuts"`
	Unsupported []string   `json:"unsupported"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Rule struct {
	ID       string   `json:"id"`
	When     string   `json:"when"`
	Label    string   `json:"label"`
	Apply    []string `json:"apply"`
	Display  string   `json:"display"`
	NextFire string   `json:"next_fire"`
}

type Shortcut struct {
	Keys   string `json:"keys"`
	Action string `json:"action"`
}

func (r rulesResult) render() {
	state := map[bool]string{true: "running", false: "paused"}[r.Auto]
	fmt.Printf("automation %s at %s\n", state, fmt.Sprintf("%.2f,%.2f", r.Location.Lat, r.Location.Lon))
	for _, rule := range r.Rules {
		next := rule.NextFire
		if next == "" {
			next = "on " + rule.When
		}
		fmt.Printf("  %-11s → %-20s next: %s\n", rule.Label, strings.Join(rule.Apply, ","), next)
	}
	for _, sc := range r.Shortcuts {
		fmt.Printf("  %-11s → %s (assign keys in system settings)\n", sc.Keys, sc.Action)
	}
	for _, u := range r.Unsupported {
		fmt.Println("  note:", u)
	}
}

func autoStatus() error {
	var res rulesResult
	if err := call("rules.list", map[string]any{}, &res); err != nil {
		return err
	}
	if jsonFlag {
		return printJSON(res)
	}
	res.render()
	return nil
}

func autoSet(on bool) error {
	var res struct {
		Auto bool `json:"auto"`
	}
	if err := call("auto.set", map[string]any{"on": on}, &res); err != nil {
		return err
	}
	if !quietFlag {
		fmt.Println(map[bool]string{true: "automation running", false: "automation paused"}[res.Auto])
	}
	return nil
}

func fieldNames(s automation.ProfileSection) []string {
	fs := make([]string, 0, len(s.Fields))
	for f := range s.Fields {
		fs = append(fs, f)
	}
	sort.Strings(fs)
	return fs
}

func editFile(path string) error {
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "vi"
	}
	c := exec.Command(ed, path)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
