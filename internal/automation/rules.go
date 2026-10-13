package automation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Rule is one automation trigger. App rules (KWin watcher) parse but stay inactive until
// milestone 4; When/Apply decide the rest.
type Rule struct {
	ID      string   `toml:"id" json:"id"` // optional; content-derived when empty
	When    string   `toml:"when" json:"when"`
	Apply   []string `toml:"apply" json:"apply"`
	Display string   `toml:"display,omitempty" json:"display,omitempty"`
	Class   []string `toml:"class,omitempty" json:"class,omitempty"` // app rules, milestone 4
}

// Shortcut pairs an action with keys. The portal lets the user assign the actual keys in
// the desktop settings; Keys is the suggestion shown there, not enforced.
type Shortcut struct {
	Keys    string `toml:"keys" json:"keys"`
	Action  string `toml:"action" json:"action"`
	Default bool   `toml:"-" json:"default,omitempty"` // a built-in suggestion, not written to rules.toml
}

// DefaultShortcuts are used when rules.toml declares no [[shortcut]] at all, so the keys
// are there to assign out of the box. Adding any shortcut of your own takes over.
func DefaultShortcuts() []Shortcut {
	return []Shortcut{
		{Keys: "Meta+Shift+Up", Action: "brightness set +10", Default: true},
		{Keys: "Meta+Shift+Down", Action: "brightness set -10", Default: true},
		{Keys: "Meta+Shift+B", Action: "bluelight toggle", Default: true},
	}
}

// Config is rules.toml.
type Config struct {
	Location struct {
		Lat float64 `toml:"lat"`
		Lon float64 `toml:"lon"`
	} `toml:"location" json:"location"`
	Rule        []Rule     `toml:"rule" json:"rule"`
	Shortcut    []Shortcut `toml:"shortcut" json:"shortcut"`
	Unsupported []string   `toml:"-" json:"unsupported,omitempty"` // rules we can't run (yet)
}

func ConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "ddcdeck"
	}
	return filepath.Join(dir, "ddcdeck")
}

func ConfigPath() string { return filepath.Join(ConfigDir(), "rules.toml") }

var hhmm = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`)

// LoadConfig reads rules.toml; a missing file is an empty config (defaults applied).
// Location defaults to Amsterdam, the bluelight script's default, until the user sets one.
func LoadConfig() (Config, error) {
	var c Config
	b, err := os.ReadFile(ConfigPath())
	switch {
	case errors.Is(err, os.ErrNotExist): // no file yet: defaults only
	case err != nil:
		return c, err
	default:
		if err := toml.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", ConfigPath(), err)
		}
	}
	if c.Location.Lat == 0 && c.Location.Lon == 0 {
		c.Location.Lat, c.Location.Lon = 52.37, 4.90
	}
	for i := range c.Rule {
		if c.Rule[i].ID == "" {
			c.Rule[i].ID = fmt.Sprintf("%s|%v|%s", c.Rule[i].When, c.Rule[i].Apply, c.Rule[i].Display)
		}
		if err := c.Rule[i].validate(); err != nil {
			return c, fmt.Errorf("rule %d: %w", i+1, err)
		}
	}
	for i := range c.Shortcut {
		if c.Shortcut[i].Action == "" || c.Shortcut[i].Keys == "" {
			return c, fmt.Errorf("shortcut %d: keys and action are both required", i+1)
		}
	}
	if len(c.Shortcut) == 0 { // nothing configured: the built-in suggestions are shown instead
		c.Shortcut = DefaultShortcuts()
	}
	return c, nil
}

func (r Rule) validate() error {
	switch {
	case len(r.Apply) == 0:
		return errors.New("apply is required")
	case r.When == "sunset" || r.When == "sunrise" || r.When == "connected" || r.When == "disconnected" || r.When == "app":
		return nil
	case hhmm.MatchString(r.When):
		return nil
	}
	return fmt.Errorf("unknown when %q (sunset, sunrise, HH:MM, connected, disconnected)", r.When)
}

// SaveConfig writes rules.toml. Comments in the file are lost; the file is meant to be
// small and generated mostly by ddcdeck itself.
func SaveConfig(c Config) error {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	// the built-in shortcut suggestions are display-only: writing them would make them
	// real entries the user did not ask for
	c.Shortcut = slices.DeleteFunc(slices.Clone(c.Shortcut), func(s Shortcut) bool { return s.Default })
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), b, 0o600)
}

// timeRule returns the parsed HH:MM trigger.
func timeRule(when string) (hour, minute int, ok bool) {
	if !hhmm.MatchString(when) {
		return 0, 0, false
	}
	h, m, _ := strings.Cut(when, ":")
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	return hh, mm, true
}

// NextFire computes when a rule will next trigger (time rules only; events return zero).
func (r Rule) NextFire(c Config, now time.Time) time.Time {
	switch r.When {
	case "sunset":
		return NextCrossing(c.Location.Lat, c.Location.Lon, now, true)
	case "sunrise":
		return NextCrossing(c.Location.Lat, c.Location.Lon, now, false)
	case "connected", "disconnected", "app":
		return time.Time{}
	}
	h, m, ok := timeRule(r.When)
	if !ok {
		return time.Time{}
	}
	fire := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !fire.After(now) {
		fire = fire.Add(24 * time.Hour)
	}
	return fire
}

// WhenLabel renders a trigger for UIs.
func WhenLabel(when string) string {
	if h, m, ok := timeRule(when); ok {
		return fmt.Sprintf("%02d:%02d", h, m)
	}
	return when
}
