package automation

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Profile is ~/.config/ddcdeck/profiles/<name>.toml: partial monitor states.
// Section keys match a monitor's model, serial or ID, or "*" for all.
type Profile struct {
	Name     string           `json:"name"`
	Monitors []ProfileSection `json:"monitors"`
}

type ProfileSection struct {
	Match   string         `json:"match"`
	Fields  map[string]any `json:"fields,omitempty"`
	OnApply []string       `json:"on_apply,omitempty"`
}

func ProfilesDir() string { return filepath.Join(ConfigDir(), "profiles") }

// LoadProfiles lists every profile in the profiles dir, sorted by name.
func LoadProfiles() ([]Profile, error) {
	des, err := os.ReadDir(ProfilesDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []Profile
	for _, de := range des {
		if de.IsDir() || filepath.Ext(de.Name()) != ".toml" {
			continue
		}
		p, err := LoadProfile(strings.TrimSuffix(de.Name(), ".toml"))
		if err != nil {
			return ps, fmt.Errorf("%s: %w", de.Name(), err)
		}
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps, nil
}

// badProfileName rejects names that would not stay inside the profiles dir.
func badProfileName(name string) bool {
	return name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, "..")
}

// LoadProfile reads one profile. Unknown TOML types in fields are an error, not a crash.
func LoadProfile(name string) (Profile, error) {
	p := Profile{Name: name}
	if badProfileName(name) {
		return p, fmt.Errorf("bad profile name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(ProfilesDir(), name+".toml"))
	if os.IsNotExist(err) {
		return p, fmt.Errorf("no profile named %q", name)
	}
	if err != nil {
		return p, err
	}
	var top map[string]map[string]map[string]any
	if err := toml.Unmarshal(b, &top); err != nil {
		return p, err
	}
	for match, sec := range top["monitor"] { // design format: [monitor.<match>]
		s := ProfileSection{Match: match, Fields: map[string]any{}}
		for k, v := range sec {
			switch k {
			case "on_apply":
				list, ok := v.([]any)
				if !ok {
					return p, fmt.Errorf("[%s] on_apply must be a list of strings", match)
				}
				for _, c := range list {
					cs, ok := c.(string)
					if !ok {
						return p, fmt.Errorf("[%s] on_apply must be a list of strings", match)
					}
					s.OnApply = append(s.OnApply, cs)
				}
			default:
				switch v.(type) {
				case int64, string:
					s.Fields[k] = v
				case float64: // TOML floats become ints when whole; reject the rest
					if v.(float64) == float64(int64(v.(float64))) {
						s.Fields[k] = int64(v.(float64))
					} else {
						return p, fmt.Errorf("[%s] %s: %v is not a whole number or name", match, k, v)
					}
				default:
					return p, fmt.Errorf("[%s] %s: value must be a number or a name string", match, k)
				}
			}
		}
		p.Monitors = append(p.Monitors, s)
	}
	sort.Slice(p.Monitors, func(i, j int) bool { return order(p.Monitors[i].Match) < order(p.Monitors[j].Match) })
	return p, nil
}

// order applies "*" sections first so exact matches win where both set a field.
func order(match string) int {
	if match == "*" {
		return 0
	}
	return 1
}

// SaveProfile writes a profile, merging into an existing file: fields for a match are
// replaced, other sections and on_apply lists are kept.
func SaveProfile(p Profile) error {
	if badProfileName(p.Name) {
		return fmt.Errorf("bad profile name %q", p.Name)
	}
	if err := os.MkdirAll(ProfilesDir(), 0o700); err != nil {
		return err
	}
	existing, _ := LoadProfile(p.Name) // missing file = empty
	byMatch := map[string]ProfileSection{}
	for _, s := range existing.Monitors {
		byMatch[s.Match] = s
	}
	for _, s := range p.Monitors {
		old := byMatch[s.Match]
		if len(s.Fields) == 0 { // nothing to store now: keep old fields (on_apply may be new)
			s.Fields = old.Fields
		}
		if len(s.OnApply) == 0 {
			s.OnApply = old.OnApply
		}
		byMatch[s.Match] = s
	}
	merged := Profile{Name: p.Name}
	for _, s := range byMatch {
		merged.Monitors = append(merged.Monitors, s)
	}
	sort.Slice(merged.Monitors, func(i, j int) bool { return merged.Monitors[i].Match < merged.Monitors[j].Match })
	var b strings.Builder
	for _, s := range merged.Monitors {
		fmt.Fprintf(&b, "[monitor.%q]\n", s.Match)
		keys := make([]string, 0, len(s.Fields))
		for k := range s.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if name, ok := s.Fields[k].(string); ok {
				fmt.Fprintf(&b, "%s = %q\n", k, name)
			} else {
				fmt.Fprintf(&b, "%s = %v\n", k, s.Fields[k])
			}
		}
		for _, c := range s.OnApply {
			fmt.Fprintf(&b, "on_apply = [%q]\n", c)
		}
		b.WriteByte('\n')
	}
	return WriteProfile(p)
}

// WriteProfile replaces the file outright — what editing needs (SaveProfile merges, so a
// removed field would survive it). The caller validates.
func WriteProfile(p Profile) error {
	if badProfileName(p.Name) {
		return fmt.Errorf("bad profile name %q", p.Name)
	}
	if err := os.MkdirAll(ProfilesDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ProfilesDir(), p.Name+".toml"), []byte(renderProfile(p)), 0o600)
}

// renderProfile serializes a profile in the documented rules format.
func renderProfile(p Profile) string {
	var b strings.Builder
	for _, s := range p.Monitors {
		fmt.Fprintf(&b, "[monitor.%q]\n", s.Match)
		keys := make([]string, 0, len(s.Fields))
		for k := range s.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if name, ok := s.Fields[k].(string); ok {
				fmt.Fprintf(&b, "%s = %q\n", k, name)
			} else {
				fmt.Fprintf(&b, "%s = %v\n", k, s.Fields[k])
			}
		}
		for _, c := range s.OnApply {
			fmt.Fprintf(&b, "on_apply = [%q]\n", c)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// DeleteProfile removes a profile file.
func DeleteProfile(name string) error {
	if badProfileName(name) {
		return fmt.Errorf("bad profile name %q", name)
	}
	err := os.Remove(filepath.Join(ProfilesDir(), name+".toml"))
	if os.IsNotExist(err) {
		return fmt.Errorf("no profile named %q", name)
	}
	return err
}

// matches reports whether a profile section applies to this monitor identity.
func matches(match, model, serial, id string) bool {
	return match == "*" || strings.EqualFold(match, model) || strings.EqualFold(match, serial) || strings.EqualFold(match, id)
}

// applied is the outcome of applying one profile to one monitor.
type applied struct {
	Display string   `json:"display"`
	Profile string   `json:"profile"`
	Fields  []string `json:"applied,omitempty"`
	Skipped []string `json:"skipped,omitempty"` // unsupported on this monitor
	Error   string   `json:"error,omitempty"`
	Hooks   []string `json:"hooks,omitempty"` // commands that ran
}

// Caller is the monitor-facing half of the API (deck.Service implements it).
type Caller interface {
	ListMonitors() []Monitor
	Set(display, feature string, value any) error
}

// Monitor is the identity subset automation needs.
type Monitor struct {
	ID, Model, Serial string
}

// ApplyProfile applies profiles in order to every connected monitor they match,
// then runs on_apply hooks (no shell, args split on spaces, 10 s timeout each).
// Each section's hooks run once per apply — on the first monitor it matches — not once
// per monitor, so a "*" section doesn't fire its command N times on N screens.
func ApplyProfile(c Caller, names []string) []applied {
	mons := c.ListMonitors()
	var out []applied
	for _, name := range names {
		p, err := LoadProfile(name)
		if err != nil {
			out = append(out, applied{Profile: name, Error: err.Error()})
			continue
		}
		ranHooks := map[string]bool{} // section match → its on_apply already ran
		for _, m := range mons {
			res := applied{Display: m.ID, Profile: name}
			matched := false
			for _, s := range p.Monitors {
				if !matches(s.Match, m.Model, m.Serial, m.ID) {
					continue
				}
				matched = true
				for _, f := range slices.Sorted(maps.Keys(s.Fields)) {
					if err := c.Set(m.ID, f, s.Fields[f]); err != nil {
						if strings.Contains(err.Error(), "support") {
							res.Skipped = append(res.Skipped, f)
						} else {
							res.Error = err.Error()
						}
						continue
					}
					res.Fields = append(res.Fields, f)
				}
				if !ranHooks[s.Match] {
					ranHooks[s.Match] = true
					for _, cmd := range s.OnApply {
						if err := runHook(cmd); err != nil {
							res.Error = err.Error()
						} else {
							res.Hooks = append(res.Hooks, cmd)
						}
					}
				}
			}
			if matched {
				out = append(out, res)
			}
		}
	}
	return out
}
