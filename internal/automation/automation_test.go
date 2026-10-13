package automation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

// fakeBus is a DDC backend stand-in, so engine tests run the real Service.
type fakeBus struct {
	mu   sync.Mutex
	vals map[byte]ddc.Value
	sets int
}

func (f *fakeBus) Detect() ([]ddc.Display, error) {
	return []ddc.Display{{Bus: 1, Connector: "DP-1", Monitor: "AUS:PA279CRV:S1"}}, nil
}
func (f *fakeBus) Capabilities(int) (ddc.Caps, error) {
	return ddc.Caps{0x10: nil, 0xE6: nil, 0x60: {0x0f, 0x11, 0x1a}}, nil
}
func (f *fakeBus) Get(_ int, codes ...byte) (map[byte]ddc.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[byte]ddc.Value{}
	for _, c := range codes {
		if v, ok := f.vals[c]; ok {
			out[c] = v
		}
	}
	return out, nil
}
func (f *fakeBus) Set(_ int, c byte, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.vals[c] = ddc.Value{Cur: v, Max: f.vals[c].Max}
	return nil
}

// newEnv isolates config, profiles, cache and DDC in a temp dir; cleanup on test end.
func newEnv(t *testing.T) (*Engine, *deck.Service, *fakeBus) {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"", "/profiles"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	models, err := model.Load() // the PA279CRV model file supplies bluelight
	if err != nil {
		t.Fatal(err)
	}
	bus := &fakeBus{vals: map[byte]ddc.Value{0x10: {Cur: 40, Max: 100}, 0xE6: {Cur: 0, Max: 20}, 0x60: {Cur: 0x1a}}}
	svc := deck.New(bus, models)
	if _, err := svc.Scan(); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(svc)
	e.stateFile = filepath.Join(t.TempDir(), "engine.json")
	e.state = engineState{Auto: true}
	return e, svc, bus
}

func writeConfig(t *testing.T, c Config) {
	t.Helper()
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(ConfigPath())
	_ = fi
}

func writeProfile(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(ProfilesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProfilesDir(), name+".toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestElevationSanity(t *testing.T) {
	ams := func(y, mo, d, h int) time.Time { return time.Date(y, time.Month(mo), d, h, 0, 0, 0, time.UTC) }
	cases := []struct {
		t    time.Time
		want float64 // sign
	}{
		{ams(2025, 6, 21, 11), +1}, // summer midday
		{ams(2025, 6, 21, 23), -1}, // summer midnight
		{ams(2025, 12, 21, 12), +1},
		{ams(2025, 12, 21, 0), -1},
	}
	for _, c := range cases {
		if got := Elevation(52.37, 4.90, c.t); got*c.want <= 0 {
			t.Errorf("elevation at %v = %.1f, want sign %.0f", c.t, got, c.want)
		}
	}
}

func TestNextCrossing(t *testing.T) {
	day := time.Date(2025, 6, 21, 12, 0, 0, 0, time.UTC) // Amsterdam: sunset ~22:00 CEST
	set := NextCrossing(52.37, 4.90, day, true)
	if set.IsZero() || !IsNight(52.37, 4.90, set) || IsNight(52.37, 4.90, set.Add(-90*time.Second)) {
		t.Fatalf("next sunset: %v", set)
	}
	if d := set.Sub(day); d < 8*time.Hour || d > 12*time.Hour {
		t.Fatalf("next sunset %v after noon, implausible", d)
	}
	rise := NextCrossing(52.37, 4.90, set.Add(time.Hour), false) // already night: next sunrise
	if rise.IsZero() || IsNight(52.37, 4.90, rise) || !rise.After(set) {
		t.Fatalf("next sunrise after a sunset: %v (sunset %v)", rise, set)
	}
	// asking for a sunset while it is already night must give the *next* evening
	next := NextCrossing(52.37, 4.90, set.Add(time.Hour), true)
	if !next.After(rise) {
		t.Fatalf("sunset while night must come after the sunrise: next %v rise %v", next, rise)
	}
}

func TestRulesParseAndNextFire(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	c := Config{}
	if _, err := toml.Decode(`
[location]
lat = 52.37
lon = 4.90

[[rule]]
when = "sunset"
apply = ["night"]

[[rule]]
when = "07:30"
apply = ["day"]

[[shortcut]]
keys = "Meta+Shift+B"
action = "bluelight toggle"
`, &c); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rule) != 2 || len(got.Shortcut) != 1 || got.Location.Lat != 52.37 {
		t.Fatalf("round trip: %+v", got)
	}
	now := time.Date(2025, 10, 13, 8, 0, 0, 0, time.Local)
	if nf := got.Rule[1].NextFire(got, now); nf.Sub(now) != 23*time.Hour+30*time.Minute {
		t.Fatalf("07:30 rule next fire from 08:00: %v", nf)
	}
	if nf := got.Rule[0].NextFire(got, now); nf.IsZero() || !IsNight(got.Location.Lat, got.Location.Lon, nf) {
		t.Fatalf("sunset next fire: %v", nf)
	}
}

func TestProfileApplyAndSave(t *testing.T) {
	e, _, bus := newEnv(t)
	writeProfile(t, "night", `
[monitor."*"]
brightness = 25

[monitor.PA279CRV]
bluelight = 20
contrast = 55
on_apply = ["/bin/true"]
`)
	res := ApplyProfile(e.svcAutomation(), []string{"night", "missing"})
	if len(res) != 2 || res[1].Error == "" {
		t.Fatalf("missing profile should error: %+v", res)
	}
	r := res[0]
	if fmt.Sprint(r.Fields) != "[brightness bluelight]" || len(r.Skipped) != 1 || r.Skipped[0] != "contrast" {
		t.Fatalf("applied=%v skipped=%v (contrast is not in caps)", r.Fields, r.Skipped)
	}
	bus.mu.Lock()
	got := bus.vals[0xE6].Cur
	bus.mu.Unlock()
	if got != 20 {
		t.Fatalf("bluelight after apply: %d", got)
	}

}

func TestEngineTimeRuleAndPause(t *testing.T) {
	e, _, bus := newEnv(t)
	h, m := time.Now().Hour(), time.Now().Minute()
	when := fmt.Sprintf("%02d:%02d", h, m) // fires now (catch-up window)
	writeConfig(t, Config{Rule: []Rule{{When: when, Apply: []string{"night"}}}})
	writeProfile(t, "night", "[monitor.\"*\"]\nbluelight = 20\n")
	e.tick(time.Now().Add(time.Minute))
	waitFor(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return bus.vals[0xE6].Cur == 20
	})
	// auto paused: no further fires
	e.SetAuto(false)
	bus.mu.Lock()
	v := bus.vals[0xE6]
	v.Cur = 0
	bus.vals[0xE6] = v
	bus.sets = 0
	bus.mu.Unlock()
	e.tick(time.Now().Add(2 * time.Minute))
	bus.mu.Lock()
	stopped := bus.sets == 0 && bus.vals[0xE6].Cur == 0
	bus.mu.Unlock()
	if !stopped {
		t.Fatal("paused engine must not fire")
	}
}

func TestEngineHotplug(t *testing.T) {
	e, _, bus := newEnv(t)
	e.HotplugDelay = 10 * time.Millisecond
	writeConfig(t, Config{Rule: []Rule{{When: "connected", Apply: []string{"coding"}}}})
	writeProfile(t, "coding", "[monitor.\"*\"]\nbrightness = 77\n")
	e.Hotplug("AUS-PA279CRV-S1", true)
	waitFor(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return bus.vals[0x10].Cur == 77
	})
}

func TestRunAction(t *testing.T) {
	e, _, bus := newEnv(t)
	if err := e.RunAction("bluelight toggle"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return bus.vals[0xE6].Cur == 20
	})
	if err := e.RunAction("nonsense"); err == nil {
		t.Fatal("bad action should error")
	}
}

func TestAPI(t *testing.T) {
	e, svc, _ := newEnv(t)
	Register(svc, e)
	writeProfile(t, "night", "[monitor.\"*\"]\nbluelight = 20\n")

	// save current → file exists with model section
	raw, _ := json.Marshal(map[string]any{"name": "now"})
	if _, err := svc.Handle("profile.save", raw); err != nil {
		t.Fatalf("profile.save: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(ProfilesDir(), "now.toml"))
	if err != nil || len(b) == 0 {
		t.Fatalf("saved profile: %v %q", err, b)
	}

	// apply both, then rules/auto
	raw, _ = json.Marshal(map[string]any{"profiles": []string{"now", "night"}})
	if _, err := svc.Handle("profile.apply", raw); err != nil {
		t.Fatal(err)
	}
	var rl struct {
		Auto  bool `json:"auto"`
		Rules []struct {
			NextFire string `json:"next_fire"`
		} `json:"rules"`
	}
	raw, _ = json.Marshal(nil)
	res, err := svc.Handle("rules.list", raw)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := json.Marshal(res)
	if err := json.Unmarshal(rb, &rl); err != nil {
		t.Fatal(err)
	}
	if !rl.Auto {
		t.Fatal("auto should default on")
	}
	raw, _ = json.Marshal(map[string]any{"on": false})
	r, err := svc.Handle("auto.set", raw)
	if err != nil {
		t.Fatal(err)
	}
	if m := r.(map[string]bool); m["auto"] {
		t.Fatal("auto.set(false) should return auto=false")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestAppRuleApplyAndRevert(t *testing.T) {
	e, _, bus := newEnv(t)
	writeConfig(t, Config{Rule: []Rule{{
		When: "app", Class: []string{"darktable", "gimp"}, Apply: []string{"photo"},
	}}})
	writeProfile(t, "photo", "[monitor.\"*\"]\nbrightness = 90\nbluelight = 20\n")

	e.OnApp("kitty") // unrelated app: nothing happens
	bus.mu.Lock()
	base := bus.vals[0x10].Cur
	bus.mu.Unlock()
	if base == 90 {
		t.Fatal("unrelated class applied the profile")
	}

	e.OnApp("darktable")
	waitFor(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return bus.vals[0x10].Cur == 90 && bus.vals[0xE6].Cur == 20
	})

	e.OnApp("darktable") // second window of the same rule: no re-apply, no revert
	e.OnApp("kitty")
	waitFor(t, func() bool { // blur: values restored to what they were before
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return bus.vals[0x10].Cur == base && bus.vals[0xE6].Cur == 0
	})
}

func TestPauseSurvivesSunTransition(t *testing.T) {
	e, _, bus := newEnv(t)
	writeConfig(t, Config{Rule: []Rule{{When: "sunset", Apply: []string{"night"}}}})
	writeProfile(t, "night", "[monitor.\"*\"]\nbluelight = 20\n")
	e.mu.Lock()
	night := IsNight(e.cfg.Location.Lat, e.cfg.Location.Lon, time.Now())
	e.state.Sun = map[bool]string{true: "day", false: "night"}[night] // opposite of reality
	e.mu.Unlock()
	e.SetAuto(false)
	e.tick(time.Now()) // paused while the sun "crosses"
	if bus.vals[0xE6].Cur != 0 {
		t.Fatal("paused engine fired")
	}
	e.SetAuto(true)
	e.tick(time.Now()) // resume: the missed transition fires
	waitFor(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return bus.vals[0xE6].Cur == 20
	})
}
