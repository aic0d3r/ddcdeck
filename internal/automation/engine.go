package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aic0d3r/ddcdeck/internal/deck"
)

// Engine schedules and fires rules. It owns no DDC state: everything goes through the
// daemon's own API methods, so writes get the same cache, coalescing and events as the CLI.
type Engine struct {
	svc       *deck.Service
	stateFile string // tests redirect; production: ~/.cache/ddcdeck/engine.json

	mu     sync.Mutex
	auto   bool
	cfg    Config
	cfgMod time.Time
	state  engineState

	appRule *Rule                     // app rule currently applied, nil when idle
	appSnap map[string]map[string]int // values before it was applied, by display → feature

	HotplugDelay time.Duration // settle time before connected rules fire (tests shrink it)
}

// engineState survives restarts so transitions that happen while the daemon is down
// still fire on the next start (same behaviour as the bluelight script's state file).
type engineState struct {
	Auto  bool            `json:"auto"`
	Sun   string          `json:"sun"`             // "day" | "night" | "" unknown
	Fired map[string]bool `json:"fired,omitempty"` // per-rule: fired for today's date
}

func statePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "ddcdeck", "engine.json")
}

// NewEngine creates the engine and loads persisted state (auto defaults to on).
func NewEngine(svc *deck.Service) *Engine {
	e := &Engine{svc: svc, HotplugDelay: 3 * time.Second, stateFile: statePath()}
	e.state.Auto = true
	if b, err := os.ReadFile(e.stateFile); err == nil {
		_ = json.Unmarshal(b, &e.state)
	}
	e.cfg, _ = LoadConfig()
	return e
}

func (e *Engine) saveState() {
	if err := os.MkdirAll(filepath.Dir(e.stateFile), 0o700); err != nil {
		slog.Warn("engine state not saved", "err", err)
		return
	}
	b, err := json.Marshal(e.state)
	if err == nil {
		if err = os.WriteFile(e.stateFile, b, 0o600); err != nil {
			slog.Warn("engine state not saved", "err", err)
		}
	}
}

// reload picks up rules.toml edits (tracked by mtime, checked every tick).
func (e *Engine) reload() {
	fi, err := os.Stat(ConfigPath())
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil && fi.ModTime() != e.cfgMod {
		if cfg, err := LoadConfig(); err != nil {
			slog.Warn("rules.toml invalid, keeping previous", "err", err)
		} else {
			e.cfg, e.cfgMod = cfg, fi.ModTime()
			slog.Info("rules reloaded", "rules", len(cfg.Rule))
		}
	}
}

// Auto reports whether automation is running.
func (e *Engine) Auto() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.Auto
}

// SetAuto pauses or resumes all triggers (manual profile applies keep working).
func (e *Engine) SetAuto(on bool) bool {
	e.mu.Lock()
	e.state.Auto = on
	e.mu.Unlock()
	e.saveState()
	e.svc.Emit(deck.Event{Event: "auto", State: map[bool]string{true: "running", false: "paused"}[on]})
	return on
}

// Run evaluates rules every tick until ctx is done: sun transitions and fixed times.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	e.tick(time.Now()) // baseline (or catch-up) immediately
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			e.tick(now)
		}
	}
}

func (e *Engine) tick(now time.Time) {
	e.reload()
	var fire []Rule
	e.mu.Lock()
	auto, cfg := e.state.Auto, e.cfg
	// sun state, persisted: a transition that happened while we were down still fires
	if night := IsNight(cfg.Location.Lat, cfg.Location.Lon, now); auto {
		want := map[bool]string{true: "night", false: "day"}[night]
		if e.state.Sun == "" {
			e.state.Sun = want // first run ever: baseline, no fire
		} else if e.state.Sun != want {
			e.state.Sun = want
			fire = append(fire, rulesWhen(cfg, map[bool]string{true: "sunset", false: "sunrise"}[night])...)
		}
	}
	// paused: keep the last known sun state, so a transition that happened while
	// paused still fires on resume (same catch-up as a daemon restart)
	for _, r := range cfg.Rule {
		h, m, ok := timeRule(r.When)
		if !ok {
			continue
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
		key := fmt.Sprintf("%s@%s", r.ID, now.Format("2006-01-02"))
		if auto && !now.Before(today) && !e.state.Fired[key] && now.Sub(today) < 24*time.Hour {
			if e.state.Fired == nil {
				e.state.Fired = map[string]bool{}
			}
			e.state.Fired[key] = true
			fire = append(fire, r)
		}
	}
	// prune fired entries older than yesterday (bounded map)
	for k := range e.state.Fired {
		if d := strings.Split(k, "@"); len(d) == 2 && d[1] != now.Format("2006-01-02") {
			delete(e.state.Fired, k)
		}
	}
	e.mu.Unlock()
	e.saveState()
	for _, r := range fire { // outside the lock: applying touches I2C for seconds
		e.fire(r)
	}
}

// rulesWhen returns the rules with a given When.
func rulesWhen(cfg Config, when string) []Rule {
	var out []Rule
	for _, r := range cfg.Rule {
		if r.When == when {
			out = append(out, r)
		}
	}
	return out
}

func (e *Engine) fire(r Rule) {
	slog.Info("rule fired", "when", r.When, "apply", r.Apply, "display", r.Display)
	e.svc.Emit(deck.Event{Event: "rule", State: r.When, Display: r.Display})
	res := ApplyProfile(e.svcAutomation(), r.Apply)
	for _, a := range res {
		if a.Error != "" {
			slog.Warn("profile apply failed", "display", a.Display, "profile", a.Profile, "err", a.Error)
		}
	}
}

// OnApp applies the first app rule matching the focused window class and remembers the
// touched values; when focus moves to a non-matching window they are restored.
func (e *Engine) OnApp(class string) {
	e.reload()
	e.mu.Lock()
	auto, cfg := e.state.Auto, e.cfg
	same := e.appRule
	e.mu.Unlock()
	if !auto {
		return
	}
	match := matchAppRule(cfg, class)
	if match != nil && same != nil && match.ID == same.ID {
		return // same rule, focus moved between its windows
	}
	if same != nil {
		e.revertApp()
	}
	if match == nil {
		return
	}
	e.mu.Lock()
	e.appRule = match
	e.appSnap = e.snapshotFor(match.Apply)
	e.mu.Unlock()
	e.fire(*match)
}

// revertApp restores the values the active app rule overrode.
func (e *Engine) revertApp() {
	e.mu.Lock()
	snap := e.appSnap
	e.appRule, e.appSnap = nil, nil
	e.mu.Unlock()
	for display, fields := range snap {
		names := make([]string, 0, len(fields))
		for f := range fields {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			if _, err := e.svc.Handle("set", mustJSON(deck.Params{Display: display, Feature: f, Value: mustJSON(fields[f])})); err != nil {
				slog.Warn("app rule revert failed", "display", display, "feature", f, "err", err)
			}
		}
	}
}

// snapshotFor reads the current values of every field the profiles will touch.
func (e *Engine) snapshotFor(profiles []string) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, name := range profiles {
		p, err := LoadProfile(name)
		if err != nil {
			continue
		}
		for _, m := range e.svcAutomation().ListMonitors() {
			for _, sec := range p.Monitors {
				if !matches(sec.Match, m.Model, m.Serial, m.ID) {
					continue
				}
				if out[m.ID] == nil {
					out[m.ID] = map[string]int{}
				}
				for f := range sec.Fields {
					if _, taken := out[m.ID][f]; taken {
						continue
					}
					raw, _ := json.Marshal(deck.Params{Display: m.ID, Feature: f})
					if r, err := e.svc.Handle("get", raw); err == nil {
						if fv, ok := r.(deck.FeatureValue); ok {
							out[m.ID][f] = fv.Value
						}
					}
				}
			}
		}
	}
	return out
}

func matchAppRule(cfg Config, class string) *Rule {
	for i, r := range cfg.Rule {
		if r.When != "app" {
			continue
		}
		for _, c := range r.Class {
			if strings.EqualFold(c, class) {
				return &cfg.Rule[i]
			}
		}
	}
	return nil
}

// Hotplug applies connected/disconnected rules after the settle delay.
func (e *Engine) Hotplug(displayID string, connected bool) {
	e.reload() // rules.toml may have changed since the last tick
	when := map[bool]string{true: "connected", false: "disconnected"}[connected]
	delay := e.HotplugDelay
	go func() {
		time.Sleep(delay)
		e.mu.Lock()
		auto, cfg := e.state.Auto, e.cfg
		e.mu.Unlock()
		if !auto {
			return
		}
		for _, r := range cfg.Rule {
			if r.When == when && (r.Display == "" || strings.EqualFold(r.Display, displayID) ||
				strings.Contains(strings.ToLower(displayID), strings.ToLower(r.Display))) {
				e.fire(r)
			}
		}
	}()
}

// svcAutomation adapts deck.Service to the profile Caller.
func (e *Engine) svcAutomation() deckAutomation {
	return deckAutomation{svc: e.svc}
}

type deckAutomation struct{ svc *deck.Service }

func (d deckAutomation) ListMonitors() []Monitor {
	raw, _ := d.svc.Handle("list", nil)
	res, _ := raw.(deck.ListResult)
	var out []Monitor
	for _, m := range res.Monitors {
		out = append(out, Monitor{ID: m.ID, Model: m.Model, Serial: m.Serial})
	}
	return out
}

func (d deckAutomation) Set(display, feature string, value any) error {
	raw, _ := json.Marshal(deck.Params{Display: display, Feature: feature, Value: mustJSON(value)})
	_, err := d.svc.Handle("set", raw)
	return err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// RunAction executes a shortcut/tray action string: "profile apply a b",
// "<feature> on|off|toggle" or "<feature> set <value>".
func (e *Engine) RunAction(action string) error {
	f := strings.Fields(action)
	switch {
	case len(f) >= 3 && f[0] == "profile" && f[1] == "apply":
		res := ApplyProfile(e.svcAutomation(), f[2:])
		for _, a := range res {
			if a.Error != "" {
				return errors.New(a.Display + ": " + a.Error)
			}
		}
		return nil
	case len(f) == 2 && (f[1] == "on" || f[1] == "off" || f[1] == "toggle"):
		_, err := e.svc.Handle("set", mustJSON(deck.Params{Feature: f[0], Value: mustJSON(f[1])}))
		return err
	case len(f) == 3 && f[1] == "set":
		_, err := e.svc.Handle("set", mustJSON(deck.Params{Feature: f[0], Value: mustJSON(f[2])}))
		return err
	}
	return fmt.Errorf("can't run action %q", action)
}

// runHook runs an on_apply command: no shell, fields split on spaces, output logged.
func runHook(cmd string) error {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return errors.New("empty on_apply command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, f[0], f[1:]...).CombinedOutput()
	if len(out) > 0 {
		slog.Info("on_apply output", "cmd", cmd, "out", strings.TrimSpace(string(out)))
	}
	if err != nil {
		return fmt.Errorf("%s: %v", cmd, err)
	}
	return nil
}
