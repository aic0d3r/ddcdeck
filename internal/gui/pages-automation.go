package gui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/aic0d3r/ddcdeck/internal/deck"
)

// RulesInfo mirrors the rules.list result the GUI needs.
type RulesInfo struct {
	Auto        bool           `json:"auto"`
	Location    Location       `json:"location"`
	Rules       []RuleInfo     `json:"rules"`
	Shortcuts   []ShortcutInfo `json:"shortcuts"`
	Unsupported []string       `json:"unsupported"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type RuleInfo struct {
	When     string   `json:"when"`
	Label    string   `json:"label"`
	Apply    []string `json:"apply"`
	Display  string   `json:"display"`
	NextFire string   `json:"next_fire"`
	LastFire string   `json:"last_fire"`
}

type ShortcutInfo struct {
	Keys    string `json:"keys"`
	Action  string `json:"action"`
	Default bool   `json:"default"` // built-in suggestion, not from rules.toml
}

// buildProfiles: apply/list profiles, save the current state as a new one.
func buildProfiles(w *window) *adw.PreferencesPage {
	g := adw.NewPreferencesGroup()
	g.SetTitle("Profiles")
	g.SetDescription("Partial monitor states. Applying changes only the fields a profile stores.")
	w.profGroup = g
	w.rebuildProfiles()
	save := gtk.NewButtonWithLabel("Save current as…")
	save.SetHAlign(gtk.AlignEnd)
	save.AddCSSClass("suggested-action")
	save.ConnectClicked(func() { w.saveProfileDialog() })
	g.SetHeaderSuffix(save)
	return prefsPage(g)
}

func (w *window) rebuildProfiles() {
	g := w.profGroup
	if g == nil {
		return
	}
	clearGroup(g, &w.profRows)
	var res struct {
		Profiles []struct {
			Name     string `json:"name"`
			Monitors []struct {
				Match   string         `json:"match"`
				Fields  map[string]any `json:"fields"`
				OnApply []string       `json:"on_apply"`
			} `json:"monitors"`
		} `json:"profiles"`
	}
	w.a.call("profile.list", map[string]any{}, &res, func(err error) {
		if err != nil {
			w.a.toast(err.Error())
			return
		}
		if len(res.Profiles) == 0 {
			r := placeholderRow("No profiles yet", "Use “Save current as…” to store this monitor's settings.")
			w.profRows = append(w.profRows, r)
			g.Add(r)
			return
		}
		for _, p := range res.Profiles {
			p := p
			var parts []string
			for _, s := range p.Monitors {
				parts = append(parts, fmt.Sprintf("%s: %s", s.Match, strings.Join(keysOf(s.Fields), ", ")))
			}
			row := adw.NewActionRow()
			row.SetTitle(p.Name)
			row.SetSubtitle(strings.Join(parts, " · "))
			btn := gtk.NewButtonWithLabel("Apply")
			btn.SetVAlign(gtk.AlignCenter)
			btn.ConnectClicked(func() {
				w.a.call("profile.apply", map[string]any{"profiles": []string{p.Name}}, nil, func(err error) {
					if err == nil {
						w.a.toast("Applied " + p.Name)
					} else {
						w.a.toast(err.Error())
					}
				})
			})
			row.AddSuffix(btn)
			w.profRows = append(w.profRows, row)
			g.Add(row)
		}
	})
}

func keysOf(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }

// saveProfileDialog asks for a name and which features to store.
func (w *window) saveProfileDialog() {
	a := w.a
	if a.cur == nil {
		return
	}
	dlg := adw.NewAlertDialog("Save profile", "Name and fields to store for "+a.cur.Model)
	dlg.AddResponse("cancel", "Cancel")
	dlg.AddResponse("save", "Save")
	dlg.SetResponseAppearance("save", adw.ResponseSuggested)
	dlg.SetDefaultResponse("save")

	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(6)
	name := gtk.NewEntry()
	name.SetPlaceholderText("Profile name")
	setA11yLabel(name, "Profile name")
	box.Append(name)
	checks := map[string]*gtk.CheckButton{}
	var lead *gtk.CheckButton
	for _, f := range sortedFeatureNames(a.cur) {
		c := gtk.NewCheckButton()
		c.SetLabel(title(f))
		c.SetActive(true)
		if lead == nil {
			lead = c
		} else {
			c.SetGroup(lead)
		}
		checks[f] = c
		box.Append(c)
	}
	dlg.SetExtraChild(box)
	dlg.ConnectResponse(func(answer string) {
		if answer != "save" {
			return
		}
		if strings.TrimSpace(name.Text()) == "" {
			a.toast("Profile needs a name")
			return
		}
		var fields []string
		for _, f := range sortedFeatureNames(a.cur) {
			if checks[f].Active() {
				fields = append(fields, f)
			}
		}
		a.call("profile.save", map[string]any{"name": name.Text(), "fields": fields}, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.toast("Saved " + name.Text())
			w.rebuildProfiles()
		})
	})
	dlg.Present(&w.win.Window)
}

func sortedFeatureNames(m *deck.Monitor) []string { return slices.Sorted(maps.Keys(m.Features)) }

// buildAutomation: pause switch, location, rule list, shortcuts.
func buildAutomation(w *window) *adw.PreferencesPage {
	a := w.a
	auto := adw.NewPreferencesGroup()
	auto.SetTitle("Automation")
	sw := adw.NewSwitchRow()
	sw.SetTitle("Enable automation")
	sw.SetSubtitle("Pauses all triggers; manual changes keep working")
	sw.Connect("notify::active", func() {
		if !a.syncing {
			a.setAuto(sw.Active())
		}
	})
	a.autoSwitch = sw
	auto.Add(sw)

	loc := adw.NewPreferencesGroup()
	loc.SetTitle("Location")
	loc.SetDescription("Used for sunset and sunrise times")
	lat := adw.NewSpinRowWithRange(-90, 90, 0.5)
	lat.SetTitle("Latitude")
	lat.SetDigits(2)
	lon := adw.NewSpinRowWithRange(-180, 180, 0.5)
	lon.SetTitle("Longitude")
	lon.SetDigits(2)
	for _, r := range []*adw.SpinRow{lat, lon} {
		r.ConnectActivated(func() { a.saveLocation(lat.Value(), lon.Value()) })
	}
	a.latRow, a.lonRow = lat, lon
	loc.Add(lat)
	loc.Add(lon)

	rules := adw.NewPreferencesGroup()
	rules.SetTitle("Rules")
	a.rulesGroup = rules

	sc := adw.NewPreferencesGroup()
	sc.SetTitle("Shortcuts")
	sc.SetDescription("Global shortcuts are assigned in System Settings → Shortcuts → DDC Deck")
	a.shortcutsGroup = sc

	return prefsPage(auto, loc, rules, sc)
}

// refreshAutomation fills the rules and shortcuts groups from the last rules.list.
func (a *App) refreshAutomation() {
	var res RulesInfo
	a.call("rules.list", map[string]any{}, &res, func(err error) {
		if err != nil {
			return // daemon without automation (old binary) — leave the page as is
		}
		a.syncing = true
		a.rules = &res
		if a.autoSwitch != nil {
			a.autoSwitch.SetActive(res.Auto)
		}
		a.updateTrayPause(res.Auto)
		if a.latRow != nil {
			a.latRow.SetValue(res.Location.Lat)
			a.lonRow.SetValue(res.Location.Lon)
		}
		clearGroup(a.rulesGroup, &a.ruleRows)
		if a.rulesGroup != nil {
			if len(res.Rules) == 0 {
				a.addRuleRow(placeholderRow("No rules", "Add rules to ~/.config/ddcdeck/rules.toml"))
			}
			for _, rule := range res.Rules {
				r := adw.NewActionRow()
				r.SetTitle(fmt.Sprintf("%s → %s", ruleLabel(rule), strings.Join(rule.Apply, ", ")))
				sub := "fires on " + rule.When
				if rule.NextFire != "" {
					sub = "next: " + rule.NextFire
				}
				if rule.LastFire != "" {
					sub += " · last fired: " + rule.LastFire // answers "did it run or is it broken?"
				}
				if rule.Display != "" {
					sub += " · " + rule.Display
				}
				r.SetSubtitle(sub)
				a.addRuleRow(r)
			}
		}
		clearGroup(a.shortcutsGroup, &a.shortcutRows)
		if a.shortcutsGroup != nil {
			if a.shortcutsErr != "" {
				a.addShortcutRow(placeholderRow("Shortcuts unavailable", a.shortcutsErr))
			}
			for _, s := range res.Shortcuts {
				r := adw.NewActionRow()
				r.SetTitle(fmt.Sprintf("%s → %s", s.Keys, s.Action))
				if s.Default {
					r.SetSubtitle("suggested — assign the keys in the system settings, or add your own [[shortcut]]")
				} else {
					r.SetSubtitle("assign the keys in the system settings")
				}
				a.addShortcutRow(r)
			}
		}
		a.syncing = false
	})
}

func ruleLabel(r RuleInfo) string {
	switch r.When {
	case "sunset", "sunrise":
		return strings.ToUpper(r.When[:1]) + r.When[1:]
	case "connected", "disconnected":
		return "Monitor " + r.When
	}
	return r.Label
}

// setAuto pauses/resumes via the API (switches and tray sync through the auto event).
func (a *App) setAuto(on bool) {
	a.call("auto.set", map[string]any{"on": on}, nil, func(err error) {
		if err != nil {
			a.toast(err.Error())
			a.refreshAutomation()
		}
	})
}

func (a *App) saveLocation(lat, lon float64) {
	a.call("location.set", map[string]any{"lat": lat, "lon": lon}, nil, func(err error) {
		if err != nil {
			a.toast(err.Error())
			return
		}
		a.toast("Location saved")
	})
}

// clearGroup removes only the rows we added (groups also parent internal header widgets,
// which must not be removed).
func clearGroup[T gtk.Widgetter](g *adw.PreferencesGroup, rows *[]T) {
	if g == nil {
		return
	}
	for _, r := range *rows {
		g.Remove(any(r).(gtk.Widgetter))
	}
	*rows = nil
}

func placeholderRow(title, subtitle string) *adw.ActionRow {
	r := adw.NewActionRow()
	r.SetTitle(title)
	r.SetSubtitle(subtitle)
	return r
}
