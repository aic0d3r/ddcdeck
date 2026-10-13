// Package model maps friendly feature names to VCP codes: MCCS defaults plus per-model
// files (data/models/*.toml, embedded; user files in $XDG_DATA_HOME/ddcdeck/models win).
package model

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/aic0d3r/ddcdeck/data"
)

// Feature is one controllable setting.
type Feature struct {
	VCP      int            `toml:"vcp" json:"vcp"`
	Type     string         `toml:"type" json:"type"` // "range" | "enum"
	Min      int            `toml:"min" json:"min,omitempty"`
	Max      int            `toml:"max" json:"max,omitempty"` // 0 = use the monitor's reported max
	Off      *int           `toml:"off" json:"off,omitempty"` // range: value meaning "off", enables on/off/toggle
	On       *int           `toml:"on" json:"on,omitempty"`   // range: value for "on" (default: max)
	Values   map[string]int `toml:"values" json:"values,omitempty"`
	Verified bool           `toml:"verified" json:"verified"`
}

// Model is a per-monitor definition file.
type Model struct {
	Mfg      string             `toml:"mfg"`
	Model    string             `toml:"model"`
	Product  int                `toml:"product"`
	Features map[string]Feature `toml:"feature"`
}

// Defaults are the standard MCCS features every monitor may have.
func Defaults() map[string]Feature {
	return map[string]Feature{
		"brightness": {VCP: 0x10, Type: "range", Max: 100, Verified: true},
		"contrast":   {VCP: 0x12, Type: "range", Max: 100, Verified: true},
		"sharpness":  {VCP: 0x87, Type: "range", Verified: true},
		"colortemp": {VCP: 0x14, Type: "enum", Verified: true, Values: map[string]int{
			"srgb": 0x01, "native": 0x02, "4000k": 0x03, "5000k": 0x04, "6500k": 0x05, "7500k": 0x06,
			"8200k": 0x07, "9300k": 0x08, "10000k": 0x09, "11500k": 0x0a, "user1": 0x0b, "user2": 0x0c, "user3": 0x0d,
		}},
		"input": {VCP: 0x60, Type: "enum", Verified: true, Values: map[string]int{
			"vga1": 0x01, "vga2": 0x02, "dvi1": 0x03, "dvi2": 0x04, "dp1": 0x0f, "dp2": 0x10, "hdmi1": 0x11, "hdmi2": 0x12,
		}},
	}
}

// Load reads the embedded models plus user models. Bad files are skipped and reported in err.
func Load() ([]Model, error) {
	var models []Model
	var errs []error
	add := func(fsys fs.FS, name string) {
		b, err := fs.ReadFile(fsys, name)
		if err == nil {
			var m Model
			if _, err = toml.Decode(string(b), &m); err == nil {
				err = m.validate()
			}
			if err == nil {
				models = append([]Model{m}, models...) // later (user) files take precedence in Match
				return
			}
		}
		errs = append(errs, fmt.Errorf("model %s: %w", name, err))
	}
	embedded, _ := fs.Glob(data.Models, "models/*.toml")
	for _, n := range embedded {
		add(data.Models, n)
	}
	dir := filepath.Join(dataHome(), "ddcdeck", "models")
	user, _ := fs.Glob(os.DirFS(dir), "*.toml")
	for _, n := range user {
		add(os.DirFS(dir), n)
	}
	return models, errors.Join(errs...)
}

func dataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share")
}

func (m Model) validate() error {
	if m.Mfg == "" || m.Model == "" {
		return errors.New("mfg and model are required")
	}
	for name, f := range m.Features {
		if f.VCP < 0 || f.VCP > 0xff {
			return fmt.Errorf("feature %s: vcp %#x out of range", name, f.VCP)
		}
		if f.Type != "range" && f.Type != "enum" {
			return fmt.Errorf("feature %s: type must be range or enum", name)
		}
		if f.Type == "enum" && len(f.Values) == 0 {
			return fmt.Errorf("feature %s: enum needs values", name)
		}
	}
	return nil
}

// Match returns the model for an EDID identity. product 0 (unknown) matches on name only.
func Match(models []Model, mfg, name string, product int) *Model {
	for i, m := range models {
		if strings.EqualFold(m.Mfg, mfg) && strings.EqualFold(m.Model, name) && (product == 0 || m.Product == product) {
			return &models[i]
		}
	}
	return nil
}

// Resolve merges defaults with the model's features. With caps, standard features the monitor
// doesn't list are dropped and their enum values trimmed to the listed ones; model features
// are trusted as-is (vendor codes are often missing from the capabilities string).
func Resolve(m *Model, caps map[byte][]int) map[string]Feature {
	out := map[string]Feature{}
	for name, f := range Defaults() {
		listed, ok := caps[byte(f.VCP)]
		if caps != nil && !ok {
			continue
		}
		if f.Type == "enum" && len(listed) > 0 {
			for k, v := range f.Values {
				if !slices.Contains(listed, v) {
					delete(f.Values, k)
				}
			}
		}
		out[name] = f
	}
	if m != nil {
		for name, f := range m.Features {
			out[name] = f
		}
	}
	return out
}
