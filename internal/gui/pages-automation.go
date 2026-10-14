package gui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/aic0d3r/ddcdeck/internal/deck"
)

// RulesInfo mirrors the rules.list result the GUI needs.
type RulesInfo struct {
	Auto         bool           `json:"auto"`
	Location     Location       `json:"location"`
	LocationName string         `json:"location_name"`
	Rules        []RuleInfo     `json:"rules"`
	Shortcuts    []ShortcutInfo `json:"shortcuts"`
	Unsupported  []string       `json:"unsupported"`
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type RuleInfo struct {
	ID       string   `json:"id"`
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
			edit := gtk.NewButtonFromIconName("document-edit-symbolic")
			edit.SetVAlign(gtk.AlignCenter)
			edit.SetTooltipText("Edit this profile")
			edit.ConnectClicked(func() { w.editProfileDialog(profileFromList(p)) })
			del := gtk.NewButtonFromIconName("user-trash-symbolic")
			del.SetVAlign(gtk.AlignCenter)
			del.SetTooltipText("Remove this profile")
			del.ConnectClicked(func() {
				confirm := adw.NewAlertDialog("Remove "+p.Name+"?", "The stored settings are deleted. The monitor keeps its current values.")
				confirm.AddResponse("cancel", "Cancel")
				confirm.AddResponse("remove", "Remove")
				confirm.SetResponseAppearance("remove", adw.ResponseDestructive)
				confirm.SetDefaultResponse("cancel")
				confirm.ConnectResponse(func(answer string) {
					if answer != "remove" {
						return
					}
					w.a.call("profile.rm", map[string]any{"name": p.Name}, nil, func(err error) {
						if err != nil {
							w.a.toast(err.Error())
							return
						}
						w.a.toast("Removed " + p.Name)
						w.rebuildProfiles()
					})
				})
				confirm.Present(&w.win.Window)
			})
			row.AddSuffix(edit)
			row.AddSuffix(del)
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
	loc.SetDescription("City for sunset and sunrise times — defaults to your timezone's city")
	a.placeRow = adw.NewActionRow()
	a.placeRow.SetTitle("City")
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("Amsterdam, Tokyo, or 52.37 4.90")
	entry.SetWidthChars(18)
	setA11yLabel(entry, "Location: city or coordinates")
	a.zoneStore = gtk.NewTreeStore([]glib.Type{glib.TypeString})
	completion := gtk.NewEntryCompletion()
	completion.SetModel(a.zoneStore)
	completion.SetTextColumn(0)
	entry.SetCompletion(completion)
	setLoc := func(params map[string]any) {
		a.call("location.set", params, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.toast("Location saved")
		})
	}
	entry.Connect("activate", func() {
		if q := strings.TrimSpace(entry.Text()); q != "" {
			setLoc(map[string]any{"query": q})
		}
	})
	btn := gtk.NewButtonWithLabel("Set")
	btn.AddCSSClass("suggested-action")
	btn.SetVAlign(gtk.AlignCenter)
	btn.ConnectClicked(func() {
		if q := strings.TrimSpace(entry.Text()); q != "" {
			setLoc(map[string]any{"query": q})
		}
	})
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.Append(entry)
	box.Append(btn)
	a.placeRow.AddSuffix(box)
	a.placeEntry = entry
	loc.Add(a.placeRow)
	a.loadZones()

	rules := adw.NewPreferencesGroup()
	rules.SetTitle("Rules")
	add := gtk.NewButtonWithLabel("Add…")
	add.SetVAlign(gtk.AlignCenter)
	add.ConnectClicked(func() { w.addRuleDialog() })
	rules.SetHeaderSuffix(add)
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
		if a.autoToggle != nil {
			syncAutoToggle(a.autoToggle, res.Auto)
		}
		a.updateTrayPause(res.Auto)
		if a.placeRow != nil {
			now := fmt.Sprintf("%.2f, %.2f", res.Location.Lat, res.Location.Lon)
			if res.LocationName != "" {
				now = res.LocationName + " — " + now
			}
			a.placeRow.SetSubtitle("Sun times use: " + now)
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
				del := gtk.NewButtonFromIconName("user-trash-symbolic")
				del.SetVAlign(gtk.AlignCenter)
				del.SetTooltipText("Remove this rule")
				id := rule.ID
				del.ConnectClicked(func() {
					a.call("rule.rm", map[string]any{"id": id}, nil, func(err error) {
						if err != nil {
							a.toast(err.Error())
							return
						}
						a.toast("Rule removed")
						a.refreshAutomation()
					})
				})
				r.AddSuffix(del)
				a.addRuleRow(r)
			}
		}
		clearGroup(a.shortcutsGroup, &a.shortcutRows)
		if a.shortcutsGroup != nil {
			if a.shortcutsErr != "" {
				a.addShortcutRow(placeholderRow("Global shortcuts unavailable", a.shortcutsErr))
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

// loadZones fills the city completion once: ~400 zone names straight from tzdata.
func (a *App) loadZones() {
	if a.zoneStore == nil || a.zoneStore.IterNChildren(nil) > 0 {
		return
	}
	var zs []struct {
		Zone string `json:"zone"`
	}
	a.call("location.zones", map[string]any{}, &zs, func(err error) {
		if err != nil {
			return // old daemon: plain text entry still works
		}
		names := make([]string, 0, len(zs))
		for _, z := range zs {
			names = append(names, z.Zone[strings.LastIndex(z.Zone, "/")+1:])
		}
		slices.Sort(names)
		for _, n := range names {
			a.zoneStore.SetValue(a.zoneStore.Append(nil), 0, glib.NewValue(n))
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

// addRuleDialog creates a rule with the triggers that need no TOML: sun, a daily time,
// and hotplug. App rules (class lists) stay in rules.toml.
func (w *window) addRuleDialog() {
	a := w.a
	var res struct {
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	a.call("profile.list", map[string]any{}, &res, func(err error) {
		if err != nil {
			a.toast(err.Error())
			return
		}
		if len(res.Profiles) == 0 {
			a.toast("Save a profile first — a rule applies one")
			return
		}
		triggers := []struct{ label, when string }{
			{"At sunset", "sunset"},
			{"At sunrise", "sunrise"},
			{"Every day at", "time"},
			{"When a monitor connects", "connected"},
			{"When a monitor disconnects", "disconnected"},
		}
		profiles := make([]string, len(res.Profiles))
		for i, p := range res.Profiles {
			profiles[i] = p.Name
		}

		dlg := adw.NewAlertDialog("Add rule", "When this happens, apply a profile")
		dlg.AddResponse("cancel", "Cancel")
		dlg.AddResponse("add", "Add")
		dlg.SetResponseAppearance("add", adw.ResponseSuggested)
		dlg.SetDefaultResponse("add")

		box := gtk.NewBox(gtk.OrientationVertical, 12)
		box.SetMarginTop(6)

		labels := make([]string, len(triggers))
		for i, t := range triggers {
			labels[i] = t.label
		}
		trig := gtk.NewDropDownFromStrings(labels)
		slim(trig, "flat")
		setA11yLabel(trig, "Trigger")
		trigRow := adw.NewActionRow()
		trigRow.SetTitle("Trigger")
		trigRow.AddSuffix(trig)
		trigRow.SetActivatableWidget(trig)

		time := gtk.NewEntry()
		time.SetPlaceholderText("21:30")
		setA11yLabel(time, "Time of day")
		timeRow := adw.NewActionRow()
		timeRow.SetTitle("Time")
		timeRow.SetSubtitle("24-hour clock, like 21:30")
		timeRow.AddSuffix(time)
		timeRow.SetVisible(false) // only for "Every day at"

		prof := gtk.NewDropDownFromStrings(profiles)
		slim(prof, "flat")
		setA11yLabel(prof, "Profile to apply")
		profRow := adw.NewActionRow()
		profRow.SetTitle("Apply profile")
		profRow.AddSuffix(prof)
		profRow.SetActivatableWidget(prof)

		valid := func() bool { // guard Add before the server has to say no
			if triggers[trig.Selected()].when != "time" {
				return true
			}
			h, m, ok := strings.Cut(strings.TrimSpace(time.Text()), ":")
			if !ok {
				return false
			}
			hh, err1 := strconv.Atoi(h)
			mm, err2 := strconv.Atoi(m)
			return err1 == nil && err2 == nil && hh < 24 && mm < 60
		}
		gate := func() { dlg.SetResponseEnabled("add", valid()) }
		time.Connect("changed", gate)
		trig.Connect("notify::selected", func() {
			timeRow.SetVisible(triggers[trig.Selected()].when == "time")
			gate()
		})

		box.Append(trigRow)
		box.Append(timeRow)
		box.Append(profRow)
		dlg.SetExtraChild(box)
		gate()
		dlg.ConnectResponse(func(answer string) {
			if answer != "add" {
				return
			}
			t := triggers[trig.Selected()]
			when := t.when
			if when == "time" {
				when = strings.TrimSpace(time.Text())
			}
			name := profiles[prof.Selected()]
			a.call("rule.add", map[string]any{"when": when, "apply": []string{name}}, nil, func(err error) {
				if err != nil {
					a.toast(err.Error())
					return
				}
				a.toast("Rule added: " + t.label + " → " + name)
				a.refreshAutomation()
			})
		})
		dlg.Present(&w.win.Window)
	})
}

// profileFromList copies the JSON-decoded profile into the API's profile type.
func profileFromList(p struct {
	Name     string `json:"name"`
	Monitors []struct {
		Match   string         `json:"match"`
		Fields  map[string]any `json:"fields"`
		OnApply []string       `json:"on_apply"`
	} `json:"monitors"`
}) (prof struct {
	Name     string `json:"name"`
	Monitors []struct {
		Match   string         `json:"match"`
		Fields  map[string]any `json:"fields"`
		OnApply []string       `json:"on_apply"`
	} `json:"monitors"`
}) {
	prof.Name = p.Name
	prof.Monitors = p.Monitors
	return prof
}

// editProfileDialog edits what a profile stores: rename it, change values, drop fields.
// Replacing happens wholesale (profile.set), so dropped fields stay dropped.
func (w *window) editProfileDialog(p struct {
	Name     string `json:"name"`
	Monitors []struct {
		Match   string         `json:"match"`
		Fields  map[string]any `json:"fields"`
		OnApply []string       `json:"on_apply"`
	} `json:"monitors"`
}) {
	a := w.a
	dlg := adw.NewAlertDialog("Edit profile", "Rename, adjust values, or untick a field to drop it")
	dlg.AddResponse("cancel", "Cancel")
	dlg.AddResponse("save", "Save")
	dlg.SetResponseAppearance("save", adw.ResponseSuggested)
	dlg.SetDefaultResponse("save")

	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(6)

	name := gtk.NewEntry()
	name.SetText(p.Name)
	setA11yLabel(name, "Profile name")
	nameRow := adw.NewActionRow()
	nameRow.SetTitle("Name")
	nameRow.AddSuffix(name)
	box.Append(nameRow)

	type field struct {
		check  *gtk.CheckButton
		editor gtk.Widgetter
		read   func() any
	}
	sections := []struct {
		match  string
		fields map[string]*field
	}{}

	for _, sec := range p.Monitors {
		s := struct {
			match  string
			fields map[string]*field
		}{match: sec.Match, fields: map[string]*field{}}
		g := adw.NewPreferencesGroup()
		g.SetTitle("For " + sec.Match)
		for _, fname := range keysOf(sec.Fields) {
			fname := fname
			row := adw.NewActionRow()
			row.SetTitle(title(fname))
			chk := gtk.NewCheckButton()
			chk.SetActive(true)
			row.AddPrefix(chk)
			var rd func() any
			switch v := sec.Fields[fname].(type) {
			case float64: // JSON number: a range value
				spin := gtk.NewSpinButtonWithRange(0, 65535, 1)
				spin.SetValue(v)
				setA11yLabel(spin, title(fname)+" value")
				row.AddSuffix(spin)
				rd = func() any { return int(spin.Value()) }
			case string: // an enum value name: offer the monitor's values when we know them
				f, known := a.cur.Features[fname]
				if known && len(f.Values) > 0 {
					names := slices.Sorted(maps.Keys(f.Values))
					if !slices.Contains(names, v) { // a name this monitor doesn't list: keep it choosable
						names = append(names, v)
					}
					drop := gtk.NewDropDownFromStrings(names)
					slim(drop, "flat")
					drop.SetSelected(uint(max(slices.Index(names, v), 0)))
					setA11yLabel(drop, title(fname)+" value")
					row.AddSuffix(drop)
					rd = func() any { return names[drop.Selected()] }
				} else {
					e := gtk.NewEntry()
					e.SetText(v)
					setA11yLabel(e, title(fname)+" value")
					row.AddSuffix(e)
					rd = func() any { return e.Text() }
				}
			default:
				continue
			}
			g.Add(row)
			s.fields[fname] = &field{check: chk, read: rd}
		}
		box.Append(g)
		sections = append(sections, struct {
			match  string
			fields map[string]*field
		}{s.match, s.fields})
	}
	dlg.SetExtraChild(box)
	dlg.ConnectResponse(func(answer string) {
		if answer != "save" {
			return
		}
		newName := strings.TrimSpace(name.Text())
		if newName == "" {
			a.toast("Profile needs a name")
			return
		}
		type secOut struct {
			Match   string         `json:"match"`
			Fields  map[string]any `json:"fields"`
			OnApply []string       `json:"on_apply,omitempty"`
		}
		out := struct {
			Name     string   `json:"name"`
			Monitors []secOut `json:"monitors"`
		}{Name: newName}
		kept := 0
		for i, sec := range sections {
			fields := map[string]any{}
			for fname, f := range sec.fields {
				if f.check.Active() {
					fields[fname] = f.read()
					kept++
				}
			}
			out.Monitors = append(out.Monitors, secOut{Match: sec.match, Fields: fields, OnApply: p.Monitors[i].OnApply})
		}
		if kept == 0 {
			a.toast("A profile needs at least one field")
			return
		}
		a.call("profile.set", map[string]any{"profile": out, "old_name": p.Name}, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
				return
			}
			if newName != p.Name {
				a.toast("Saved as " + newName)
			} else {
				a.toast("Saved " + newName)
			}
			w.rebuildProfiles()
		})
	})
	dlg.Present(&w.win.Window)
}
