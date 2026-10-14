package automation

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/diag"
	"github.com/aic0d3r/ddcdeck/internal/location"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

// Register wires the automation API methods into the service and forwards hotplug
// events to the engine. Call once from the daemon before Serve.
func Register(s *deck.Service, e *Engine) {
	reg := func(method string, fn func(raw json.RawMessage) (any, error)) {
		s.Extend(method, fn)
	}
	decode := func(raw json.RawMessage, p any) error {
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, p); err != nil {
			return &deck.Error{Code: deck.ErrUsage, Message: "bad params: " + err.Error()}
		}
		return nil
	}
	usage := func(format string, a ...any) error {
		return &deck.Error{Code: deck.ErrUsage, Message: fmt.Sprintf(format, a...)}
	}

	reg("profile.list", func(json.RawMessage) (any, error) {
		ps, err := LoadProfiles()
		if err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		return map[string]any{"profiles": ps}, nil
	})

	reg("profile.show", func(raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		prof, err := LoadProfile(p.Name)
		if err != nil {
			return nil, &deck.Error{Code: deck.ErrUsage, Message: err.Error()}
		}
		return prof, nil
	})

	reg("profile.apply", func(raw json.RawMessage) (any, error) {
		var p struct {
			Profiles []string `json:"profiles"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if len(p.Profiles) == 0 {
			return nil, usage("profiles is required")
		}
		results := ApplyProfile(e.svcAutomation(), p.Profiles)
		for _, a := range results {
			s.Emit(deck.Event{Event: "profile", Display: a.Display, Profile: a.Profile, State: "applied"})
		}
		return map[string]any{"results": results}, nil
	})

	reg("profile.save", func(raw json.RawMessage) (any, error) {
		var p struct {
			Name    string   `json:"name"`
			Display string   `json:"display,omitempty"`
			Fields  []string `json:"fields,omitempty"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if p.Name == "" {
			return nil, usage("name is required")
		}
		statusRaw, _ := json.Marshal(deck.Params{Display: p.Display})
		r, err := s.Handle("status", statusRaw)
		if err != nil {
			return nil, err
		}
		res, _ := r.(deck.StatusResult)
		want := map[string]bool{}
		for _, f := range p.Fields {
			want[f] = true
		}
		var mon *deck.Monitor
		if r, err := s.Handle("monitor", mustJSON(deck.Params{Display: p.Display})); err == nil {
			mon, _ = r.(*deck.Monitor)
		}
		if mon == nil {
			return nil, &deck.Error{Code: deck.ErrNoMonitor, Message: "no monitor selected"}
		}
		prof := Profile{Name: p.Name, Monitors: []ProfileSection{{Match: mon.Model, Fields: map[string]any{}}}}
		for _, fv := range res.Values {
			if fv.Feature == "reset" {
				continue // storing it would factory-reset the monitor on every apply
			}
			if len(want) > 0 && !want[fv.Feature] {
				continue
			}
			if fv.Name != "" {
				prof.Monitors[0].Fields[fv.Feature] = fv.Name
			} else {
				prof.Monitors[0].Fields[fv.Feature] = fv.Value
			}
		}
		if len(prof.Monitors[0].Fields) == 0 {
			return nil, usage("nothing to save: no supported features matched")
		}
		if err := SaveProfile(prof); err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		s.Emit(deck.Event{Event: "profile", Display: mon.ID, Profile: p.Name, State: "saved"})
		return prof, nil
	})

	reg("profile.set", func(raw json.RawMessage) (any, error) {
		var p struct {
			Profile Profile `json:"profile"`
			OldName string  `json:"old_name,omitempty"` // renaming: the file to remove after writing
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		prof := p.Profile
		if prof.Name == "" {
			return nil, usage("name is required")
		}
		fields := 0
		for i := range prof.Monitors {
			if prof.Monitors[i].Match == "" {
				prof.Monitors[i].Match = "*"
			}
			for f, v := range prof.Monitors[i].Fields {
				switch n := v.(type) { // JSON numbers arrive as float64: whole ones are ints
				case float64:
					if n != float64(int64(n)) {
						return nil, usage("%s: %v must be a whole number or a value name", f, n)
					}
					prof.Monitors[i].Fields[f] = int64(n)
				case string, int64:
				default:
					return nil, usage("%s: values must be numbers or names", f)
				}
				fields++
			}
		}
		if fields == 0 {
			return nil, usage("a profile needs at least one field — remove the profile instead of emptying it")
		}
		if err := WriteProfile(prof); err != nil { // replace, not merge: dropped fields stay dropped
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		if p.OldName != "" && p.OldName != prof.Name {
			if err := DeleteProfile(p.OldName); err != nil {
				return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
			}
		}
		s.Emit(deck.Event{Event: "profile", Profile: prof.Name, State: "saved"})
		return prof, nil
	})

	reg("profile.rm", func(raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := DeleteProfile(p.Name); err != nil {
			return nil, &deck.Error{Code: deck.ErrUsage, Message: err.Error()}
		}
		return map[string]bool{"removed": true}, nil
	})

	reg("rules.list", func(json.RawMessage) (any, error) {
		e.mu.Lock()
		cfg, auto := e.cfg, e.state.Auto
		e.mu.Unlock()
		now := time.Now()
		type ruleInfo struct {
			ID       string   `json:"id"`
			When     string   `json:"when"`
			Label    string   `json:"label"`
			Apply    []string `json:"apply"`
			Class    []string `json:"class,omitempty"`
			Display  string   `json:"display,omitempty"`
			NextFire string   `json:"next_fire,omitempty"`
			LastFire string   `json:"last_fire,omitempty"`
		}
		var rules []ruleInfo
		for _, r := range cfg.Rule {
			ri := ruleInfo{ID: r.ID, When: r.When, Label: WhenLabel(r.When), Apply: r.Apply, Class: r.Class, Display: r.Display}
			if t := r.NextFire(cfg, now); !t.IsZero() {
				ri.NextFire = t.Format("Mon 15:04")
			}
			if ts := e.LastFired(r.ID); ts != "" {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					ri.LastFire = t.Format("Mon 15:04")
				}
			}
			rules = append(rules, ri)
		}
		return map[string]any{
			"auto":          auto,
			"location":      cfg.Location,
			"location_name": location.Name(cfg.Location.Lat, cfg.Location.Lon),
			"rules":         rules,
			"shortcuts":     cfg.Shortcut,
			"unsupported":   cfg.Unsupported,
		}, nil
	})

	reg("rule.add", func(raw json.RawMessage) (any, error) {
		var p struct {
			When    string   `json:"when"`
			Apply   []string `json:"apply"`
			Display string   `json:"display,omitempty"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		r := Rule{When: p.When, Apply: p.Apply, Display: p.Display}
		if r.When == "app" {
			return nil, usage("app rules need a class list: edit rules.toml to add one")
		}
		if err := r.validate(); err != nil {
			return nil, usage("rule: %v", err)
		}
		cfg, err := LoadConfig() // fresh from disk, like location.set: never clobber concurrent edits
		if err != nil {
			return nil, usage("rules.toml is invalid, fix it first: %v", err)
		}
		cfg.Rule = append(cfg.Rule, r)
		if err := SaveConfig(cfg); err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		e.reload() // live without waiting for the next tick
		return map[string]any{"added": r.key()}, nil
	})

	reg("rule.rm", func(raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		cfg, err := LoadConfig()
		if err != nil {
			return nil, usage("rules.toml is invalid, fix it first: %v", err)
		}
		kept := cfg.Rule[:0]
		removed := false
		for _, r := range cfg.Rule {
			if r.ID == p.ID {
				removed = true
				continue
			}
			kept = append(kept, r)
		}
		if !removed {
			return nil, usage("no rule with id %q", p.ID)
		}
		cfg.Rule = kept
		if err := SaveConfig(cfg); err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		e.reload()
		return map[string]bool{"removed": true}, nil
	})

	reg("auto.set", func(raw json.RawMessage) (any, error) {
		var p struct {
			On *bool `json:"on"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if p.On == nil {
			return nil, usage("on is required")
		}
		return map[string]bool{"auto": e.SetAuto(*p.On)}, nil
	})

	reg("location.set", func(raw json.RawMessage) (any, error) {
		var p struct {
			Lat   *float64 `json:"lat"`
			Lon   *float64 `json:"lon"`
			Query string   `json:"query"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if p.Query != "" { // "Tokyo" resolves like typing it into any map widget
			z, ok := location.Find(p.Query)
			if !ok {
				return nil, usage("no place named %q — try a city (Amsterdam) or exact lat lon", p.Query)
			}
			p.Lat, p.Lon = &z.Lat, &z.Lon
		}
		if p.Lat == nil || p.Lon == nil || *p.Lat < -90 || *p.Lat > 90 || *p.Lon < -180 || *p.Lon > 180 {
			return nil, usage("lat (-90..90) and lon (-180..180) are required")
		}
		// load fresh from disk: saving the stale in-memory copy would clobber edits the
		// engine hasn't reloaded yet (or a rules.toml that currently fails to parse)
		cfg, err := LoadConfig()
		if err != nil {
			return nil, usage("rules.toml is invalid, fix it first: %v", err)
		}
		cfg.Location.Lat, cfg.Location.Lon = *p.Lat, *p.Lon
		e.mu.Lock()
		e.cfg = cfg
		e.mu.Unlock()
		if err := SaveConfig(cfg); err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		return map[string]any{"location": cfg.Location, "name": location.Name(cfg.Location.Lat, cfg.Location.Lon)}, nil
	})

	// the city picker list: every tzdata zone with its reference city, ~400 entries
	reg("location.zones", func(json.RawMessage) (any, error) { return location.Zones(), nil })

	reg("action.run", func(raw json.RawMessage) (any, error) {
		var p struct {
			Action string `json:"action"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := e.RunAction(p.Action); err != nil {
			return nil, &deck.Error{Code: deck.ErrUsage, Message: err.Error()}
		}
		return map[string]bool{"ran": true}, nil
	})

	reg("doctor", func(json.RawMessage) (any, error) {
		return diag.Run(s), nil
	})

	reg("schedule.set", func(raw json.RawMessage) (any, error) {
		var p struct {
			Spec  string `json:"spec"`
			Level int    `json:"level"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if p.Level == 0 {
			p.Level = 20
		}
		if err := SetBlueLightSchedule(e, p.Spec, p.Level); err != nil {
			return nil, usage("%v", err)
		}
		e.reload()
		return map[string]any{"schedule": p.Spec, "level": p.Level}, nil
	})

	reg("model.fetch", func(raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id,omitempty"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		id := p.ID
		if id == "" { // the monitor the caller selected, when it's the daemon's caller
			r, err := s.Handle("monitor", raw)
			if err != nil {
				return nil, err
			}
			if m, ok := r.(*deck.Monitor); ok {
				id = fmt.Sprintf("%s-%s-%d", m.Mfg, m.Model, m.Product)
			}
		}
		path, err := model.Fetch(id)
		if err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		if mm, err := model.LoadProfileModel(path); err == nil { // ddcutil users get it too
			if mkErr := os.MkdirAll(model.DdcutilDir(), 0o700); mkErr == nil {
				_ = os.WriteFile(filepath.Join(model.DdcutilDir(), id+".mccs"), []byte(mm.MCCS()), 0o600)
			}
		}
		if _, err := s.Handle("scan", nil); err != nil { // usable at once
			slog.Warn("model fetched but rescan failed", "err", err)
		}
		return map[string]string{"path": path}, nil
	})

	// hotplug events drive connected/disconnected rules
	ch, cancel := s.Subscribe()
	go func() {
		for ev := range ch {
			if ev.Event == "hotplug" {
				e.Hotplug(ev.Display, ev.State == "connected")
			}
		}
		cancel()
	}()
	slog.Debug("automation registered")
}
