package deck

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the user's global settings in ~/.config/ddcdeck/config.toml. Rules and
// profiles have their own files; this holds what is neither per-monitor state nor a rule.
type Config struct {
	// DefaultDisplay is the monitor used when -d is omitted: index, ID, model, serial
	// or connector. Empty means "the first one detected".
	DefaultDisplay string `toml:"default_display,omitempty" json:"default_display"`

	// DDC timing knobs passed to every ddcutil call; for monitors, docks or GPUs that
	// answer slowly or not at all. Restart the daemon after changing them.
	SleepMultiplier     float64 `toml:"sleep_multiplier,omitempty" json:"sleep_multiplier"`
	DisableDynamicSleep bool    `toml:"disable_dynamic_sleep,omitempty" json:"disable_dynamic_sleep"`
}

// DdcutilArgs are the global ddcutil flags the tuning keys ask for.
func (c Config) DdcutilArgs() []string {
	var a []string
	if c.SleepMultiplier > 0 {
		a = append(a, "--sleep-multiplier="+strconv.FormatFloat(c.SleepMultiplier, 'f', -1, 64))
	}
	if c.DisableDynamicSleep {
		a = append(a, "--disable-dynamic-sleep")
	}
	return a
}

func ConfigPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "ddcdeck", "config.toml")
	}
	return filepath.Join("ddcdeck", "config.toml") // same fallback as automation.ConfigDir
}

// LoadConfig reads config.toml; a missing or broken file reads as the empty config.
// Read on every use so an edit or a `ddcdeck default` from a terminal takes effect at once.
func LoadConfig() Config {
	var c Config
	if b, err := os.ReadFile(ConfigPath()); err == nil {
		_, _ = toml.Decode(string(b), &c)
	}
	return c
}

// DefaultDisplay is the pinned monitor ("" = first detected).
func DefaultDisplay() string { return LoadConfig().DefaultDisplay }

// LogPath is the log file for a daemon that runs outside systemd (the GUI starts one when
// there is no user unit), where stderr would otherwise go nowhere.
func LogPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "ddcdeck", "daemon.log")
}

// SetDefaultDisplay pins a monitor ("" clears it). Written by both the CLI and the GUI.
func SetDefaultDisplay(sel string) error {
	c := LoadConfig()
	c.DefaultDisplay = sel
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700); err != nil {
		return err
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), b, 0o600)
}

// Match returns the monitor a selector names: 1-based index, ID, model, serial or
// connector (case-insensitive). "" and no match return nil.
func Match(mons []*Monitor, sel string) *Monitor {
	if sel == "" {
		return nil
	}
	i := slices.IndexFunc(mons, func(m *Monitor) bool {
		return strconv.Itoa(m.Index) == sel || slices.ContainsFunc([]string{m.ID, m.Model, m.Serial, m.Connector},
			func(v string) bool { return v != "" && strings.EqualFold(v, sel) })
	})
	if i < 0 {
		return nil
	}
	return mons[i]
}
