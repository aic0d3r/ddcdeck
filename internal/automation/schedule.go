package automation

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// BlueLightScheduleID marks the rules that a blue-light schedule owns, so setting a new
// one replaces them without touching hand-written rules.
const BlueLightScheduleID = "bluelight-schedule"

var spanPattern = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d-([01]?\d|2[0-3]):[0-5]\d$`)

// SetBlueLightSchedule replaces any previous blue-light schedule with spec:
// "sunset", "off", or "HH:MM-HH:MM" (on at the first time, off at the second). The two
// helper profiles it applies are (re)written too, and the state right now is applied at
// once through e — setting a schedule at 13:29 must not wait until tomorrow for the
// filter to match it. e may be nil (then the rules just take effect on their own times).
func SetBlueLightSchedule(e *Engine, spec string, level int) error {
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
	if e != nil {
		// whatever state the schedule implies right now gets applied at once, without
		// waiting a day for the next scheduled edge to reach it
		state := "off"
		if spec != "off" && bluelightNow(cfg, spec, time.Now()) {
			state = "on"
		}
		_ = e.RunAction("profile apply bluelight-" + state)
	}
	return nil
}

// bluelightNow reports whether the filter should be on at time t under spec.
func bluelightNow(c Config, spec string, t time.Time) bool {
	if spec == "off" {
		return false
	}
	if spanPattern.MatchString(spec) {
		onPart, offPart, _ := strings.Cut(spec, "-")
		on, _ := time.Parse("15:04", onPart)
		off, _ := time.Parse("15:04", offPart)
		now := time.Date(2000, 1, 1, t.Hour(), t.Minute(), 0, 0, time.UTC)
		onT := time.Date(2000, 1, 1, on.Hour(), on.Minute(), 0, 0, time.UTC)
		offT := time.Date(2000, 1, 1, off.Hour(), off.Minute(), 0, 0, time.UTC)
		if onT.After(offT) { // 21:00-07:00 spans midnight
			return !now.Before(onT) || now.Before(offT)
		}
		return !now.Before(onT) && now.Before(offT)
	}
	// sun schedule: night runs from sunset to the next sunrise, so whichever event
	// comes next after t tells the side t is on: sunrise next means the sun is down
	nr := NextCrossing(c.Location.Lat, c.Location.Lon, t, false)
	ns := NextCrossing(c.Location.Lat, c.Location.Lon, t, true)
	return nr.Before(ns)
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
