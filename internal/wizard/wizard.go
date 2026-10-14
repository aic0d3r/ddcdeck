// Package wizard implements the OSD-diffing code discovery from DESIGN.md §6: snapshot all
// VCP codes, have the user change one setting in the monitor's OSD, snapshot again and diff.
// It is UI-free; the CLI (and later the GUI) drives it.
package wizard

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

// chunkSize codes per getvcp call: keeps each ddcutil invocation under its 15 s timeout.
const chunkSize = 16

// maxRounds bounds the "narrow it down" loop so a noisy monitor can't trap the user.
const maxRounds = 5

// Session watches one monitor's codes across OSD changes.
type Session struct {
	be    ddc.Backend
	bus   int
	codes []byte
	base  map[byte]ddc.Value
}

// Start reads the capabilities, picks the codes to watch (every code the monitor lists,
// plus the manufacturer range E0–FF) and drops the flaky ones by comparing two consecutive
// snapshots. Codes that never answer are dropped as well.
func Start(be ddc.Backend, bus int) (*Session, error) {
	caps, err := be.Capabilities(bus)
	if err != nil {
		return nil, fmt.Errorf("capabilities unreadable: %w", err)
	}
	set := map[byte]struct{}{}
	for c := range caps {
		switch c {
		case 0x02: // "new control value": mirrors whichever code changed last — pure noise
		case 0xC0: // display usage time: ticks on its own and pollutes every diff
		default:
			set[c] = struct{}{}
		}
	}
	for c := byte(0xE0); c != 0; c++ { // manufacturer range, wraps at FF
		set[c] = struct{}{}
	}
	codes := slices.Sorted(maps.Keys(set))
	snap1, err := read(be, bus, codes)
	if err != nil {
		return nil, err
	}
	snap2, err := read(be, bus, slices.Sorted(maps.Keys(snap1)))
	if err != nil {
		return nil, err
	}
	stable := map[byte]ddc.Value{}
	for c, v := range snap1 {
		if snap2[c].Cur == v.Cur { // value hopped on its own: not a usable signal
			stable[c] = v
		}
	}
	if len(stable) == 0 {
		return nil, fmt.Errorf("no stable VCP codes to watch")
	}
	return &Session{be: be, bus: bus, codes: slices.Sorted(maps.Keys(stable)), base: stable}, nil
}

// Codes returns the watched codes.
func (s *Session) Codes() []byte { return s.codes }

// Base returns the baseline snapshot taken at Start.
func (s *Session) Base() map[byte]ddc.Value { return s.base }

func read(be ddc.Backend, bus int, codes []byte) (map[byte]ddc.Value, error) {
	out := map[byte]ddc.Value{}
	for len(codes) > 0 {
		n := min(len(codes), chunkSize)
		vals, err := be.Get(bus, codes[:n]...)
		if err != nil && len(vals) == 0 {
			return nil, err
		}
		maps.Copy(out, vals) // codes that didn't answer are simply absent
		codes = codes[n:]
	}
	return out, nil
}

// Snapshot reads all watched codes now.
func (s *Session) Snapshot() (map[byte]ddc.Value, error) { return read(s.be, s.bus, s.codes) }

// Changed returns the codes whose current value differs from the baseline, with old and new.
func Changed(base, after map[byte]ddc.Value) map[byte][2]ddc.Value {
	out := map[byte][2]ddc.Value{}
	for c, v := range after {
		if o, ok := base[c]; ok && o.Cur != v.Cur {
			out[c] = [2]ddc.Value{o, v}
		}
	}
	return out
}

// sideEffects are standard codes that already have built-in features and that an OSD preset
// change legitimately moves (each preset loads its own brightness, gains, colour temperature).
var sideEffects = []byte{0x10, 0x12, 0x13, 0x14, 0x16, 0x18, 0x1A, 0x6C, 0x6E, 0x70}

// DropSideEffects removes preset side-effect codes from a diff so they don't make it
// ambiguous. If nothing else changed the diff is returned untouched.
func DropSideEffects(d map[byte][2]ddc.Value) map[byte][2]ddc.Value {
	out := map[byte][2]ddc.Value{}
	for c, v := range d {
		if !slices.Contains(sideEffects, c) {
			out[c] = v
		}
	}
	if len(out) == 0 {
		return d
	}
	return out
}

// Intersect keeps the codes present in both diffs (second OSD change narrows the candidate).
func Intersect(a, b map[byte][2]ddc.Value) map[byte][2]ddc.Value {
	out := map[byte][2]ddc.Value{}
	for c, v := range b {
		if _, ok := a[c]; ok {
			out[c] = v
		}
	}
	return out
}

// Finding is a confirmed candidate: the code, the value before the OSD change and after.
type Finding struct {
	Code byte
	Old  ddc.Value
	New  ddc.Value
}

// Feature turns a finding into a model entry. NC readings are enums; continuous ones are
// ranges with the monitor-reported max. Off and Values come from the driving UI.
func (f Finding) Feature(off *int, values map[string]int) model.Feature {
	feat := model.Feature{VCP: int(f.Code), Verified: true}
	if f.New.NC {
		feat.Type, feat.Values = "enum", values
	} else {
		feat.Type, feat.Max = "range", f.New.Max
		if off != nil {
			feat.Off = off
		}
	}
	return feat
}

// MaxRounds is the narrowing limit the UI should respect.
const MaxRounds = maxRounds

// Set writes a value (used to confirm a finding by reverting it through the OSD).
func (s *Session) Set(code byte, v int) error { return s.be.Set(s.bus, code, v) }

// sortedDiffCodes renders diff keys deterministically for UI messages.
func sortedDiffCodes(d map[byte][2]ddc.Value) []string {
	out := make([]string, 0, len(d))
	for c := range d {
		out = append(out, fmt.Sprintf("%02X", c))
	}
	sort.Strings(out)
	return out
}
