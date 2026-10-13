package automation

import (
	"fmt"
	"regexp"
	"strings"
)

// BlueLightScheduleID marks the rules that a blue-light schedule owns, so setting a new
// one replaces them without touching hand-written rules.
const BlueLightScheduleID = "bluelight-schedule"

var spanPattern = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d-([01]?\d|2[0-3]):[0-5]\d$`)

// SetBlueLightSchedule replaces any previous blue-light schedule with spec:
// "sunset", "off", or "HH:MM-HH:MM" (on at the first time, off at the second). The two
// helper profiles it applies are (re)written too. Used by the CLI and the GUI alike.
func SetBlueLightSchedule(spec string, level int) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	rules := cfg.Rule[:0]
	for _, r := range cfg.Rule {
		if r.ID != BlueLightScheduleID {
			rules = append(rules, r)
		}
	}
	cfg.Rule = rules
	switch {
	case spec == "sunset":
		cfg.Rule = append(cfg.Rule,
			Rule{ID: BlueLightScheduleID, When: "sunset", Apply: []string{"bluelight-on"}},
			Rule{ID: BlueLightScheduleID, When: "sunrise", Apply: []string{"bluelight-off"}},
		)
	case spec == "off":
		// removed above; the profiles are left alone
	case spanPattern.MatchString(spec):
		on, off, _ := strings.Cut(spec, "-")
		cfg.Rule = append(cfg.Rule,
			Rule{ID: BlueLightScheduleID, When: on, Apply: []string{"bluelight-on"}},
			Rule{ID: BlueLightScheduleID, When: off, Apply: []string{"bluelight-off"}},
		)
	default:
		return fmt.Errorf("expected sunset, off or HH:MM-HH:MM, got %q", spec)
	}
	if err := SaveConfig(cfg); err != nil {
		return err
	}
	if spec != "off" {
		for name, lvl := range map[string]int{"bluelight-on": level, "bluelight-off": 0} {
			if err := SaveProfile(Profile{
				Name:     name,
				Monitors: []ProfileSection{{Match: "*", Fields: map[string]any{"bluelight": lvl}}},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// BlueLightSchedule reports the active schedule: "sunset", "off", or "HH:MM-HH:MM".
func BlueLightSchedule() string {
	cfg, err := LoadConfig()
	if err != nil {
		return "off"
	}
	on, off := "", ""
	for _, r := range cfg.Rule {
		if r.ID != BlueLightScheduleID {
			continue
		}
		if h, m, ok := timeRule(r.When); ok {
			t := fmt.Sprintf("%02d:%02d", h, m)
			if len(r.Apply) > 0 && r.Apply[0] == "bluelight-on" {
				on = t
			} else {
				off = t
			}
		} else if r.When == "sunset" {
			return "sunset"
		}
	}
	if on != "" && off != "" {
		return on + "-" + off
	}
	return "off"
}
