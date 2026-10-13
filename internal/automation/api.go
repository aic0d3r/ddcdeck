package automation

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aic0d3r/ddcdeck/internal/deck"
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
			Display  string   `json:"display,omitempty"`
			NextFire string   `json:"next_fire,omitempty"`
		}
		var rules []ruleInfo
		for _, r := range cfg.Rule {
			ri := ruleInfo{ID: r.ID, When: r.When, Label: WhenLabel(r.When), Apply: r.Apply, Display: r.Display}
			if t := r.NextFire(cfg, now); !t.IsZero() {
				ri.NextFire = t.Format("Mon 15:04")
			}
			rules = append(rules, ri)
		}
		return map[string]any{
			"auto":        auto,
			"location":    cfg.Location,
			"rules":       rules,
			"shortcuts":   cfg.Shortcut,
			"unsupported": cfg.Unsupported,
		}, nil
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
			Lat *float64 `json:"lat"`
			Lon *float64 `json:"lon"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if p.Lat == nil || p.Lon == nil || *p.Lat < -90 || *p.Lat > 90 || *p.Lon < -180 || *p.Lon > 180 {
			return nil, usage("lat (-90..90) and lon (-180..180) are required")
		}
		e.mu.Lock()
		e.cfg.Location.Lat, e.cfg.Location.Lon = *p.Lat, *p.Lon
		cfg := e.cfg
		e.mu.Unlock()
		if err := SaveConfig(cfg); err != nil {
			return nil, &deck.Error{Code: deck.ErrDDC, Message: err.Error()}
		}
		return map[string]any{"location": cfg.Location}, nil
	})

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
