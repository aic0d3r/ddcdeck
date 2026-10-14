package model

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/aic0d3r/ddcdeck/data"
)

// Hint is a known vendor VCP code for an EDID manufacturer. A candidate, never applied automatically.
type Hint struct {
	Mfg    string         `toml:"mfg"`
	VCP    int            `toml:"vcp"`
	Name   string         `toml:"name"`
	Values map[string]int `toml:"values"`
	Note   string         `toml:"note"`
	Src    string         `toml:"src"`
}

// VendorHints returns the bundled hints for an EDID manufacturer id, sorted by code.
func VendorHints(mfg string) []Hint {
	var f struct {
		Hint []Hint `toml:"hint"`
	}
	if _, err := toml.Decode(string(data.Vendors), &f); err != nil {
		return nil // embedded file is covered by a test
	}
	var out []Hint
	for _, h := range f.Hint {
		if strings.EqualFold(h.Mfg, mfg) {
			out = append(out, h)
		}
	}
	slices.SortStableFunc(out, func(a, b Hint) int { return a.VCP - b.VCP })
	return out
}

func (h Hint) String() string {
	s := fmt.Sprintf("0x%02X %s", h.VCP, h.Name)
	if h.Note != "" {
		s += " — " + h.Note
	}
	return s
}
