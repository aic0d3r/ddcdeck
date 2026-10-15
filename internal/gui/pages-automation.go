package gui

import (
	"fmt"
	"html"
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
	g.SetDescription("Saved settings you can apply in one click. A profile only changes the settings it stores.")
	w.profGroup = g
	w.rebuildProfiles()
	save := gtk.NewButtonWithLabel("Save current as…")
	save.SetVAlign(gtk.AlignCenter) // unset, it stretches over the whole header
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
	w.refreshPresets()
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
			r := placeholderRow("No profiles yet", "Save this monitor's settings with “Save current as…”, or start from two examples.")
			add := gtk.NewButtonWithLabel("Add starters")
			add.SetVAlign(gtk.AlignCenter)
			add.SetTooltipText("Bright (brightness 80) and Night (brightness 15, blue light filter on). Monitors without a feature skip it.")
			add.ConnectClicked(func() { w.addStarterProfiles(add) })
			r.AddSuffix(add)
			w.profRows = append(w.profRows, r)
			g.Add(r)
			return
		}
		for _, p := range res.Profiles {
			p := p
			var parts []string
			for _, s := range p.Monitors {
				who := s.Match
				if who == "*" {
					who = "Every monitor"
				}
				parts = append(parts, fmt.Sprintf("%s: %s", who, strings.Join(keysOf(s.Fields), ", ")))
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

// addStarterProfiles writes two conservative examples for every monitor; unsupported fields are skipped on apply.
func (w *window) addStarterProfiles(btn *gtk.Button) {
	a := w.a
	btn.SetSensitive(false)
	starters := []struct {
		name   string
		fields map[string]any
	}{
		{"Bright", map[string]any{"brightness": 80}},
		{"Night", map[string]any{"brightness": 15, "bluelight": 20}},
	}
	pending := len(starters)
	for _, s := range starters {
		prof := map[string]any{"name": s.name, "monitors": []map[string]any{{"match": "*", "fields": s.fields}}}
		a.call("profile.set", map[string]any{"profile": prof}, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
			}
			if pending--; pending == 0 {
				w.rebuildProfiles()
			}
		})
	}
}

func keysOf(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }

// saveProfileDialog asks for a name and which features to store.
func (w *window) saveProfileDialog() {
	a := w.a
	if a.cur == nil {
		return
	}
	dlg, page, save := w.formDialog("Save profile", "Save", 440, 560)
	save.SetSensitive(false) // nothing to save under an empty name

	nameGroup := adw.NewPreferencesGroup()
	name := adw.NewEntryRow()
	name.SetTitle("Profile name")
	name.ConnectChanged(func() { save.SetSensitive(strings.TrimSpace(name.Text()) != "") })
	nameGroup.Add(name)
	page.Add(nameGroup)

	fields := adw.NewPreferencesGroup()
	fields.SetTitle("Settings to store")
	fields.SetDescription("From " + a.cur.Model + ". Applying a profile changes only what is ticked here.")
	checks := map[string]*adw.SwitchRow{}
	for _, f := range profileFeatures(a.cur) {
		r := adw.NewSwitchRow()
		r.SetTitle(title(f))
		// switching input or power from a one-click profile can blank the screen: opt in
		r.SetActive(f != "input" && f != "power" && f != "kvm")
		checks[f] = r
		fields.Add(r)
	}
	page.Add(fields)

	var all *adw.SwitchRow
	if len(a.mons) > 1 {
		g := adw.NewPreferencesGroup()
		all = adw.NewSwitchRow()
		all.SetTitle("Every connected monitor")
		all.SetSubtitle("Stores each monitor's own values; applying sets them all")
		g.Add(all)
		page.Add(g)
	}

	doSave := func() {
		var picked []string
		for _, f := range profileFeatures(a.cur) {
			if checks[f].Active() {
				picked = append(picked, f)
			}
		}
		if len(picked) == 0 {
			a.toast("Tick at least one setting")
			return
		}
		n := strings.TrimSpace(name.Text())
		a.call("profile.save", map[string]any{"name": n, "fields": picked, "all": all != nil && all.Active()}, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.toast("Saved " + n)
			w.rebuildProfiles()
		})
		dlg.Close()
	}
	save.ConnectClicked(doSave)
	name.ConnectEntryActivated(func() {
		if save.Sensitive() {
			doSave()
		}
	})
	dlg.SetFocus(name)
	dlg.Present(&w.win.Window)
}

// formDialog is the shared shell for long forms: Cancel and Save live in the header bar so
// they never scroll away, and the body is a scrolling preferences page.
func (w *window) formDialog(title, saveLabel string, width, height int) (*adw.Dialog, *adw.PreferencesPage, *gtk.Button) {
	dlg := adw.NewDialog()
	dlg.SetTitle(title)
	dlg.SetContentWidth(width)
	dlg.SetContentHeight(height)
	hb := adw.NewHeaderBar()
	hb.SetShowStartTitleButtons(false)
	hb.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { dlg.Close() })
	save := gtk.NewButtonWithLabel(saveLabel)
	save.AddCSSClass("suggested-action")
	hb.PackStart(cancel)
	hb.PackEnd(save)
	page := adw.NewPreferencesPage()
	tv := adw.NewToolbarView()
	tv.AddTopBar(hb)
	tv.SetContent(page)
	dlg.SetChild(tv)
	dlg.SetDefaultWidget(save)
	return dlg, page, save
}

func sortedFeatureNames(m *deck.Monitor) []string { return slices.Sorted(maps.Keys(m.Features)) }

// profileFeatures are the features a profile can store: reset is an action, not a setting.
func profileFeatures(m *deck.Monitor) []string {
	return slices.DeleteFunc(sortedFeatureNames(m), func(f string) bool { return f == "reset" })
}

// buildAutomation: pause switch, location, rule list, shortcuts.
func buildAutomation(w *window) *adw.PreferencesPage {
	a := w.a
	auto := adw.NewPreferencesGroup()
	auto.SetTitle("Automation")
	auto.SetDescription("Rules and sun times run by themselves. Pausing stops them; manual changes always work.")
	sw := adw.NewSwitchRow()
	sw.SetTitle("Run automation")
	sw.SetSubtitle("Off pauses every rule below")
	sw.Connect("notify::active", func() {
		if !a.syncing {
			a.setAuto(sw.Active())
		}
	})
	a.autoSwitch = sw
	auto.Add(sw)

	a.placeRow = adw.NewActionRow()
	a.placeRow.SetTitle("City for sunset and sunrise")
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("City or 52.37 4.90")
	entry.SetWidthChars(20)
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
			entry.SetText("")
		})
	}
	entry.Connect("activate", func() {
		if q := strings.TrimSpace(entry.Text()); q != "" {
			setLoc(map[string]any{"query": q})
		}
	})
	btn := gtk.NewButtonWithLabel("Set")
	btn.SetVAlign(gtk.AlignCenter)
	btn.SetSensitive(false) // nothing typed, nothing to set
	entry.Connect("changed", func() { btn.SetSensitive(strings.TrimSpace(entry.Text()) != "") })
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
	auto.Add(a.placeRow)
	a.loadZones()

	rules := adw.NewPreferencesGroup()
	rules.SetTitle("Rules")
	rules.SetDescription("When something happens, apply a profile")
	add := gtk.NewButtonWithLabel("Add…")
	add.SetVAlign(gtk.AlignCenter)
	add.ConnectClicked(func() { w.addRuleDialog() })
	rules.SetHeaderSuffix(add)
	a.rulesGroup = rules

	sc := adw.NewPreferencesGroup()
	sc.SetTitle("Shortcuts")
	sc.SetDescription("Global shortcuts are assigned in System Settings → Shortcuts → DDC Deck")
	a.shortcutsGroup = sc

	return prefsPage(auto, rules, sc)
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
		if a.rulesGroup != nil {
			a.rulesGroup.SetSensitive(res.Auto) // paused rules read as paused
		}
		a.updateTrayPause(res.Auto)
		a.bindShortcuts(res.Shortcuts) // no-op unless the list changed (e.g. a new input hotkey)
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
				if rule.ID == bluelightScheduleID {
					sub += " · from the Eye care schedule on the Display tab"
				}
				r.SetSubtitle(sub)
				apply := rule.Apply
				a.profileNames(func(have []string) { // a rule whose profile is gone fires into nothing: say so
					var gone []string
					for _, n := range apply {
						if !slices.Contains(have, n) {
							gone = append(gone, n)
						}
					}
					if len(gone) > 0 {
						r.AddPrefix(gtk.NewImageFromIconName("dialog-warning-symbolic"))
						r.SetSubtitle(html.EscapeString("Missing profile " + strings.Join(gone, ", ") + ": nothing will happen. " + sub))
					}
				})
				del := gtk.NewButtonFromIconName("user-trash-symbolic")
				del.SetVAlign(gtk.AlignCenter)
				del.SetTooltipText("Remove this rule")
				del.AddCSSClass("flat")
				setA11yLabel(del, "Remove rule "+ruleLabel(rule))
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
				warn := placeholderRow("Global shortcuts unavailable", html.EscapeString(a.shortcutsErr))
				warn.AddPrefix(gtk.NewImageFromIconName("dialog-warning-symbolic"))
				a.addShortcutRow(warn)
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
	a.profileNames(func(profiles []string) {
		if len(profiles) == 0 {
			a.toast("Save a profile first — a rule applies one")
			return
		}
		triggers := []struct{ label, when string }{
			{"At sunset", "sunset"},
			{"At sunrise", "sunrise"},
			{"Every day at a set time", "time"},
			{"When a monitor connects", "connected"},
			{"When a monitor disconnects", "disconnected"},
		}
		dlg, page, add := w.formDialog("New rule", "Add", 440, 290)

		labels := make([]string, len(triggers))
		for i, t := range triggers {
			labels[i] = t.label
		}
		trig := adw.NewComboRow()
		trig.SetTitle("When")
		trig.SetModel(gtk.NewStringList(labels))

		clock := adw.NewEntryRow()
		clock.SetTitle("Time (24-hour, like 21:30)")
		clock.SetVisible(false) // only for "Every day at a set time"

		prof := adw.NewComboRow()
		prof.SetTitle("Apply profile")
		prof.SetModel(gtk.NewStringList(profiles))

		g := adw.NewPreferencesGroup()
		g.SetDescription("When the trigger happens, the profile is applied automatically.")
		g.Add(trig)
		g.Add(clock)
		g.Add(prof)
		page.Add(g)

		validTime := func() bool {
			h, m, ok := strings.Cut(strings.TrimSpace(clock.Text()), ":")
			if !ok {
				return false
			}
			hh, err1 := strconv.Atoi(h)
			mm, err2 := strconv.Atoi(m)
			return err1 == nil && err2 == nil && hh >= 0 && hh < 24 && mm >= 0 && mm < 60
		}
		gate := func() { // guard Add before the server has to say no
			ok := triggers[trig.Selected()].when != "time" || validTime()
			add.SetSensitive(ok)
			if triggers[trig.Selected()].when == "time" && !ok && strings.TrimSpace(clock.Text()) != "" {
				clock.AddCSSClass("error")
			} else {
				clock.RemoveCSSClass("error")
			}
		}
		clock.ConnectChanged(gate)
		trig.NotifyProperty("selected", func() {
			isTime := triggers[trig.Selected()].when == "time"
			clock.SetVisible(isTime)
			if isTime {
				clock.GrabFocus()
			}
			gate()
		})
		gate()

		doAdd := func() {
			t := triggers[trig.Selected()]
			when := t.when
			if when == "time" {
				when = strings.TrimSpace(clock.Text())
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
			dlg.Close()
		}
		add.ConnectClicked(doAdd)
		clock.ConnectEntryActivated(func() {
			if add.Sensitive() {
				doAdd()
			}
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
	dlg, page, save := w.formDialog("Edit "+p.Name, "Save", 480, 620)

	nameGroup := adw.NewPreferencesGroup()
	name := adw.NewEntryRow()
	name.SetTitle("Name")
	name.SetText(p.Name)
	name.ConnectChanged(func() { save.SetSensitive(strings.TrimSpace(name.Text()) != "") })
	nameGroup.Add(name)
	page.Add(nameGroup)

	type field struct {
		check *gtk.CheckButton
		read  func() any
	}
	sections := make([]map[string]*field, len(p.Monitors))
	for i, sec := range p.Monitors {
		sections[i] = map[string]*field{}
		g := adw.NewPreferencesGroup()
		who := "For " + sec.Match
		if sec.Match == "*" {
			who = "For every monitor"
		}
		g.SetTitle(who)
		if i == 0 {
			g.SetDescription("Untick a setting to drop it from the profile.")
		}
		for _, fname := range keysOf(sec.Fields) {
			chk := gtk.NewCheckButton()
			chk.SetActive(true)
			chk.SetVAlign(gtk.AlignCenter)
			setA11yLabel(chk, "Keep "+title(fname))
			var row gtk.Widgetter
			var rd func() any
			switch v := sec.Fields[fname].(type) {
			case float64: // JSON number: a range value
				sp := adw.NewSpinRowWithRange(0, 65535, 1)
				sp.SetTitle(title(fname))
				sp.SetValue(v)
				sp.AddPrefix(chk)
				row, rd = sp, func() any { return int(sp.Value()) }
			case string: // an enum value name: offer the monitor's values when we know them
				f, known := a.cur.Features[fname]
				if known && len(f.Values) > 0 {
					names := slices.Sorted(maps.Keys(f.Values))
					if !slices.Contains(names, v) { // a name this monitor doesn't list: keep it choosable
						names = append(names, v)
					}
					shown := make([]string, len(names))
					for j, n := range names {
						shown[j] = valueLabel(n) // "6500 K", not "6500k"
					}
					cb := adw.NewComboRow()
					cb.SetTitle(title(fname))
					cb.SetModel(gtk.NewStringList(shown))
					cb.SetSelected(uint(max(slices.Index(names, v), 0)))
					cb.AddPrefix(chk)
					row, rd = cb, func() any { return names[cb.Selected()] }
				} else {
					er := adw.NewEntryRow()
					er.SetTitle(title(fname))
					er.SetText(v)
					er.AddPrefix(chk)
					row, rd = er, func() any { return er.Text() }
				}
			default:
				continue
			}
			g.Add(row)
			sections[i][fname] = &field{check: chk, read: rd}
		}
		page.Add(g)
	}

	save.ConnectClicked(func() {
		newName := strings.TrimSpace(name.Text())
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
			for fname, f := range sec {
				if f.check.Active() {
					fields[fname] = f.read()
					kept++
				}
			}
			out.Monitors = append(out.Monitors, secOut{Match: p.Monitors[i].Match, Fields: fields, OnApply: p.Monitors[i].OnApply})
		}
		if kept == 0 {
			a.toast("A profile needs at least one setting")
			return
		}
		a.call("profile.set", map[string]any{"profile": out, "old_name": p.Name}, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.toast("Saved " + newName)
			w.rebuildProfiles()
		})
		dlg.Close()
	})
	dlg.Present(&w.win.Window)
}
