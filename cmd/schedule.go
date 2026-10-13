package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/automation"
)

// bluelightScheduleID marks the rules that `bluelight schedule` owns, so it can replace
// them without touching hand-written rules.
const bluelightScheduleID = "bluelight-schedule"

var scheduleLevel int

func init() {
	bl, _, err := rootCmd.Find([]string{"bluelight"})
	if err != nil {
		return
	}
	schedule := &cobra.Command{
		Use:   "schedule <sunset|off|HH:MM-HH:MM>",
		Short: "Blue light on/off by sun or clock, replacing any previous schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return setSchedule(a[0])
		},
	}
	schedule.Flags().IntVar(&scheduleLevel, "level", 20, "filter strength when on")
	bl.AddCommand(schedule)
}

var span = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d-([01]?\d|2[0-3]):[0-5]\d$`)

func setSchedule(spec string) error {
	cfg, err := automation.LoadConfig()
	if err != nil {
		return err
	}
	// drop previous schedule rules
	rules := cfg.Rule[:0]
	for _, r := range cfg.Rule {
		if r.ID != bluelightScheduleID {
			rules = append(rules, r)
		}
	}
	cfg.Rule = rules
	switch {
	case spec == "sunset":
		cfg.Rule = append(cfg.Rule,
			automation.Rule{ID: bluelightScheduleID, When: "sunset", Apply: []string{"bluelight-on"}},
			automation.Rule{ID: bluelightScheduleID, When: "sunrise", Apply: []string{"bluelight-off"}},
		)
	case spec == "off":
		// removed above; also leave the profiles alone
	case span.MatchString(spec):
		on, off, _ := strings.Cut(spec, "-")
		cfg.Rule = append(cfg.Rule,
			automation.Rule{ID: bluelightScheduleID, When: on, Apply: []string{"bluelight-on"}},
			automation.Rule{ID: bluelightScheduleID, When: off, Apply: []string{"bluelight-off"}},
		)
	default:
		return fmt.Errorf("expected sunset, off or HH:MM-HH:MM, got %q", spec)
	}
	if err := automation.SaveConfig(cfg); err != nil {
		return err
	}
	if spec != "off" {
		for name, level := range map[string]int{"bluelight-on": scheduleLevel, "bluelight-off": 0} {
			if err := automation.SaveProfile(automation.Profile{
				Name:     name,
				Monitors: []automation.ProfileSection{{Match: "*", Fields: map[string]any{"bluelight": level}}},
			}); err != nil {
				return err
			}
		}
	}
	if !quietFlag {
		if spec == "off" {
			fmt.Println("blue light schedule removed")
		} else {
			fmt.Printf("blue light on at %s (level %d), off rules follow; reloads within 20 s\n", spec, scheduleLevel)
		}
	}
	return nil
}
