package gui

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

// placed features have a fixed spot; anything else a model file defines lands under "More".
var placed = []string{"brightness", "contrast", "sharpness", "bluelight", "colortemp", "preset", "input", "kvm", "power", "volume", "mute"}

type page struct {
	name, title, icon string
	build             func(w *window) *adw.PreferencesPage // nil page = hidden from the sidebar
}

func pageList() []page {
	return []page{
		{"display", "Display", "display-brightness-symbolic", buildDisplay},
		{"input", "Input", "video-display-symbolic", buildInput},
		{"profiles", "Profiles", "starred-symbolic", buildProfiles},
		{"automation", "Automation", "alarm-symbolic", buildAutomation},
		{"monitor", "Monitor", "preferences-desktop-display-symbolic", buildMonitor},
		{"settings", "Settings", "emblem-system-symbolic", buildAppSettings},
	}
}

type window struct {
	a         *App
	win       *adw.ApplicationWindow
	toasts    *adw.ToastOverlay
	split     *adw.NavigationSplitView
	sidebar   *gtk.ListBox
	rows      []*gtk.ListBoxRow
	content   *adw.NavigationPage
	stack     *gtk.Stack
	status    *adw.StatusPage
	picker    *gtk.DropDown
	pickerH   coreglib.SignalHandle // handler id, blocked while the model is replaced
	banner    *adw.Banner
	bannerFor string // monitor ID the banner text was last set for (a new monitor starts at the lookup offer)
	notFound  bool   // lookup came back empty: the banner now offers discovery
	profGroup *adw.PreferencesGroup
	profRows  []*adw.ActionRow
	profList  *gtk.ListBox // sidebar: one-tap profiles
	profHead  *gtk.Label
	sideBox   *gtk.Box
	binder    *binder
}

func newWindow(a *App) *window {
	w := &window{a: a, binder: &binder{}}
	w.win = adw.NewApplicationWindow(&a.app.Application)
	w.win.SetTitle("DDC Deck")
	w.win.AddCSSClass("ddcdeck-main")
	w.win.SetDefaultSize(820, 600)
	w.win.SetHideOnClose(true) // close = back to the tray, unless the user chose otherwise
	w.win.ConnectCloseRequest(func() bool {
		if a.prefs.QuitOnClose {
			a.quit()
		}
		return false
	})
	w.win.ConnectShow(a.refresh)

	// sidebar
	w.sidebar = gtk.NewListBox()
	w.sidebar.AddCSSClass("navigation-sidebar")
	for _, p := range pageList() {
		box := gtk.NewBox(gtk.OrientationHorizontal, 12)
		box.Append(gtk.NewImageFromIconName(p.icon))
		l := gtk.NewLabel(p.title)
		l.SetXAlign(0)
		box.Append(l)
		row := gtk.NewListBoxRow()
		row.SetChild(box)
		row.SetName(p.name)
		w.sidebar.Append(row)
		w.rows = append(w.rows, row)
	}
	w.sidebar.ConnectRowSelected(func(row *gtk.ListBoxRow) { // selection, so arrow keys switch pages too
		if row != nil {
			w.show(row.Name())
		}
	})
	w.profHead = gtk.NewLabel("Quick profiles")
	w.profHead.AddCSSClass("heading")
	w.profHead.SetXAlign(0)
	w.profHead.SetMarginStart(11) // lines up with the row labels below
	w.profHead.SetMarginTop(18)
	w.profHead.SetMarginBottom(6)
	w.profList = gtk.NewListBox()
	w.profList.AddCSSClass("navigation-sidebar")
	w.profList.SetSelectionMode(gtk.SelectionNone)
	w.profList.SetActivateOnSingleClick(true)
	w.profList.ConnectRowActivated(func(row *gtk.ListBoxRow) { a.applyProfile(row.Name()) })
	w.profHead.SetVisible(false)
	w.profList.SetVisible(false)
	w.sideBox = gtk.NewBox(gtk.OrientationVertical, 0)
	w.sideBox.Append(w.sidebar)
	w.sideBox.Append(w.profHead)
	w.sideBox.Append(w.profList)
	sideTV := adw.NewToolbarView()
	sideTV.AddTopBar(adw.NewHeaderBar())
	sideTV.SetContent(w.sideBox)
	sidePage := adw.NewNavigationPage(sideTV, "DDC Deck")

	// content
	w.picker = gtk.NewDropDownFromStrings(nil)
	setA11yLabel(w.picker, "Monitor")
	w.pickerH = w.picker.Connect("notify::selected", func() { a.selectMonitor(int(w.picker.Selected())) })
	hb := adw.NewHeaderBar()
	hb.SetTitleWidget(w.picker)
	autoBtn := gtk.NewToggleButton()
	autoBtn.SetIconName("alarm-symbolic")
	autoBtn.SetTooltipText("Automation runs your rules automatically — sunset times, docking, focused apps.\nClick to pause it; manual changes always work.")
	autoBtn.ConnectToggled(func() {
		if !a.syncing {
			a.setAuto(autoBtn.Active())
		}
	})
	a.autoToggle = autoBtn
	a.syncing = true // initial state must not read as a user click (setAuto toasts)
	syncAutoToggle(autoBtn, a.rules == nil || a.rules.Auto)
	a.syncing = false
	hb.PackEnd(autoBtn)
	w.stack = gtk.NewStack()
	w.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.status = adw.NewStatusPage()
	w.status.SetIconName("video-display-symbolic")
	w.stack.AddNamed(w.status, "status")
	w.banner = adw.NewBanner("")
	w.banner.ConnectButtonClicked(w.bannerAction)
	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(w.banner)
	content.Append(w.stack)
	contentTV := adw.NewToolbarView()
	contentTV.AddTopBar(hb)
	contentTV.SetContent(content)
	w.content = adw.NewNavigationPage(contentTV, "Display")

	w.split = adw.NewNavigationSplitView()
	w.split.SetSidebar(sidePage)
	w.split.SetContent(w.content)
	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(w.split)
	w.win.SetContent(w.toasts)

	// narrow window: collapse the sidebar
	bp := adw.NewBreakpoint(adw.BreakpointConditionParse("max-width: 560sp"))
	bp.AddSetter(w.split, "collapsed", true)
	w.win.AddBreakpoint(bp)

	w.showStatus("Connecting…", "")
	return w
}

// isInfoPage: the pages that have their own lookup/discover rows, so the banner stays away.
func isInfoPage(name string) bool { return name == "monitor" || name == "settings" }

// setBanner words the no-model-file banner: first a lookup in the community database (the
// likely fix, one click), and only when that comes back empty the guided discovery.
func (w *window) setBanner() {
	if w.notFound {
		w.banner.SetTitle("Not in the community database yet. Map its features yourself, then share them.")
		w.banner.SetButtonLabel("Discover features…")
		w.banner.SetTooltipText("")
		return
	}
	w.banner.SetTitle("Standard features only. The community database may have more for this monitor.")
	w.banner.SetButtonLabel("Look up")
	if m := w.a.cur; m != nil { // say exactly what leaves the machine
		w.banner.SetTooltipText(fmt.Sprintf("Sends only the model ID (%s-%s-%d) to github.com. Nothing else.", m.Mfg, m.Model, m.Product))
	}
}

func (w *window) bannerAction() {
	a := w.a
	if w.notFound {
		a.openWizard()
		return
	}
	m := a.cur
	if m == nil {
		return
	}
	w.banner.SetSensitive(false) // in flight: no second click, layout stays put
	w.banner.SetTitle("Looking up " + m.Model + "…")
	a.call("model.fetch", map[string]any{"id": fmt.Sprintf("%s-%s-%d", m.Mfg, m.Model, m.Product)}, nil, func(err error) {
		w.banner.SetSensitive(true)
		switch {
		case err == nil:
			a.toast("Model file loaded")
			var res deck.ListResult
			a.call("list", deck.Params{}, &res, func(error) { a.setMonitors(res.Monitors) })
		case strings.Contains(err.Error(), "no community model file"): // Fetch's 404 message
			w.notFound = true
			w.setBanner()
		default: // offline, rate limited, bad file: the offer stays so the user can retry
			a.toast(err.Error())
			w.setBanner()
		}
	})
}

func (w *window) showStatus(title, detail string) {
	w.banner.SetRevealed(false)
	w.status.SetTitle(title)
	w.status.SetDescription(detail)
	w.stack.SetVisibleChildName("status")
	w.sideBox.SetSensitive(false)
	w.picker.SetVisible(false)
	w.content.SetTitle("DDC Deck")
}

// rebuild recreates all pages for the current monitor.
func (w *window) rebuild() {
	a := w.a
	visible := w.stack.VisibleChildName()
	for _, p := range pageList() {
		if old := w.stack.ChildByName(p.name); old != nil {
			w.stack.Remove(old)
		}
	}
	a.removeBinder(w.binder)
	w.binder = &binder{}
	a.addBinder(w.binder)

	first := ""
	for i, p := range pageList() {
		pg := p.build(w)
		w.rows[i].SetVisible(pg != nil)
		if pg == nil {
			continue
		}
		if first == "" {
			first = p.name
		}
		w.stack.AddNamed(pg, p.name)
	}
	if want := os.Getenv("DDCDECK_PAGE"); want != "" && w.stack.ChildByName(want) != nil {
		first = want // self-check/screenshot affordance; unset in normal use
	}

	names := make([]string, len(a.mons))
	sel := 0
	for i, m := range a.mons {
		names[i] = fmt.Sprintf("%s (%s)", m.Model, m.Connector)
		if m == a.cur {
			sel = i
		}
	}
	w.picker.HandlerBlock(w.pickerH)
	w.picker.SetModel(gtk.NewStringList(names))
	w.picker.SetSelected(uint(sel))
	w.picker.HandlerUnblock(w.pickerH)
	w.picker.SetVisible(true)
	w.sideBox.SetSensitive(true)
	if w.bannerFor != a.cur.ID {
		w.bannerFor, w.notFound = a.cur.ID, false
	}
	w.setBanner()
	w.refreshPresets()
	w.banner.SetRevealed(!a.cur.HasModel && !isInfoPage(w.stack.VisibleChildName()))

	if visible == "status" || w.stack.ChildByName(visible) == nil {
		visible = first
	}
	w.show(visible)
}

func (w *window) show(name string) {
	for i, p := range pageList() {
		if p.name == name {
			w.stack.SetVisibleChildName(name)
			w.content.SetTitle(p.title)
			w.sidebar.SelectRow(w.rows[i])
			w.split.SetShowContent(true)
		}
	}
	if w.a.cur != nil {
		w.banner.SetRevealed(!isInfoPage(name) && !w.a.cur.HasModel)
	}
}

func prefsPage(groups ...*adw.PreferencesGroup) *adw.PreferencesPage {
	var pg *adw.PreferencesPage
	for _, g := range groups {
		if g == nil {
			continue
		}
		if pg == nil {
			pg = adw.NewPreferencesPage()
		}
		pg.Add(g)
	}
	return pg
}

func buildDisplay(w *window) *adw.PreferencesPage {
	a, b := w.a, w.binder
	var more []string
	for name := range a.cur.Features {
		// reset is destructive: it lives behind a confirmation on the Settings page
		if !slices.Contains(placed, name) && name != "reset" {
			more = append(more, name)
		}
	}
	slices.Sort(more)
	return prefsPage(
		buildQuick(w),
		group(a, b, "Picture", "brightness", "contrast", "sharpness"),
		group(a, b, "Color", "preset", "colortemp"),
		buildEyeCare(w),
		group(a, b, "Audio", "volume", "mute"),
		group(a, b, "Power", "power"),
		group(a, b, "More", more...),
	)
}

// buildQuick: one-tap profiles and, with several monitors, the link switch.
func buildQuick(w *window) *adw.PreferencesGroup {
	a := w.a
	if len(a.mons) < 2 {
		return nil
	}
	g := adw.NewPreferencesGroup()
	g.SetTitle("Monitors")
	link := adw.NewSwitchRow()
	link.SetTitle("Link all monitors")
	link.SetSubtitle("Brightness, contrast and volume sliders move every monitor together")
	link.SetActive(a.prefs.LinkMonitors)
	link.NotifyProperty("active", func() {
		a.prefs.LinkMonitors = link.Active()
		a.savePrefs()
	})
	g.Add(link)
	return g
}

// profileNames reads the saved profile names.
func (a *App) profileNames(done func([]string)) {
	var res struct {
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	a.call("profile.list", map[string]any{}, &res, func(err error) {
		if err != nil {
			return
		}
		names := make([]string, len(res.Profiles))
		for i, p := range res.Profiles {
			names[i] = p.Name
		}
		done(names)
	})
}

func (a *App) applyProfile(name string) {
	a.call("profile.apply", map[string]any{"profiles": []string{name}}, nil, func(err error) {
		if err != nil {
			a.toast(err.Error())
			return
		}
		a.toast("Applied " + name)
		a.refreshFresh()
	})
}

// refreshPresets re-reads the saved profiles into the Display tab's chips; no profiles, no chips.
func (w *window) refreshPresets() {
	w.a.profileNames(func(names []string) {
		for c := w.profList.FirstChild(); c != nil; c = w.profList.FirstChild() {
			w.profList.Remove(c)
		}
		for _, n := range names {
			l := gtk.NewLabel(n)
			l.SetXAlign(0)
			l.SetEllipsize(pango.EllipsizeEnd)
			row := gtk.NewListBoxRow()
			row.SetChild(l)
			row.SetName(n)
			row.SetTooltipText("Apply " + n)
			w.profList.Append(row)
		}
		w.profHead.SetVisible(len(names) > 0)
		w.profList.SetVisible(len(names) > 0)
	})
}

// bluelightScheduleID is the rule id `ddcdeck bluelight schedule` (and the GUI's
// schedule rows) use to find its own rules again.
const bluelightScheduleID = "bluelight-schedule"

// buildEyeCare: the blue light rows plus the schedule that switches them — the GUI face
// of `ddcdeck bluelight schedule`.
func buildEyeCare(w *window) *adw.PreferencesGroup {
	a, b := w.a, w.binder
	g := group(a, b, "Eye care", "bluelight")
	if g == nil {
		return nil // no filter feature: no schedule to drive
	}
	combo := gtk.NewDropDownFromStrings([]string{"Off", "Sunset", "Fixed times"})
	slim(combo, "flat")
	combo.SetSelected(0)
	setA11yLabel(combo, "Blue light schedule")
	time := gtk.NewEntry()
	time.SetPlaceholderText("21:00-07:00")
	setA11yLabel(time, "On and off times")
	timeRow := adw.NewActionRow()
	timeRow.SetTitle("Times")
	timeRow.AddSuffix(time)
	timeRow.SetVisible(false)
	combo.Connect("notify::selected", func() { timeRow.SetVisible(combo.Selected() == 2) })

	level := adw.NewSpinRowWithRange(0, 20, 1)
	level.SetTitle("Level when scheduled")
	level.SetDigits(0)
	level.SetValue(20)

	// Set only becomes clickable once the controls differ from what is saved: it
	// rewrites the rule file, so an idle blue button begging a stray click is a trap
	spec := func() string {
		sp := map[uint]string{0: "off", 1: "sunset"}[combo.Selected()]
		if sp == "" {
			sp = strings.TrimSpace(time.Text())
		}
		return sp
	}
	save := gtk.NewButtonWithLabel("Set")
	save.AddCSSClass("suggested-action")
	save.SetVAlign(gtk.AlignCenter)
	save.SetSensitive(false)
	save.SetTooltipText("Replaces any previous schedule")
	baseSpec, baseLevel := "off", 20
	refresh := func() {
		save.SetSensitive(spec() != baseSpec || int(level.Value()) != baseLevel)
	}
	combo.Connect("notify::selected", refresh)
	time.Connect("changed", refresh)
	level.Connect("notify::value", refresh)

	save.ConnectClicked(func() {
		sp := spec()
		a.call("schedule.set", map[string]any{"spec": sp, "level": int(level.Value())}, nil, func(err error) {
			if err != nil {
				a.toast(err.Error())
				return
			}
			baseSpec, baseLevel = sp, int(level.Value())
			refresh()
			a.blueSpec = sp
			a.updateBlueSub()
			a.toast("Blue light schedule: " + sp)
		})
	})

	row := adw.NewActionRow()
	row.SetTitle("Schedule")
	row.SetSubtitle("Turns the filter on and off by itself")
	sched := gtk.NewBox(gtk.OrientationHorizontal, 8)
	sched.Append(combo)
	sched.Append(save)
	row.AddSuffix(sched)
	row.SetActivatableWidget(combo)

	g.Add(row)
	g.Add(timeRow)
	g.Add(level)

	// reflect what is configured: the schedule rules and the level profile
	var res RulesInfo
	a.call("rules.list", map[string]any{}, &res, func(error) {
		on, off := "", ""
		for _, r := range res.Rules {
			if r.ID != bluelightScheduleID {
				continue
			}
			if r.When == "sunset" || r.When == "sunrise" {
				combo.SetSelected(1)
				a.blueSpec, baseSpec = "sunset", "sunset"
				a.updateBlueSub()
				refresh()
				return
			}
			if len(r.Apply) > 0 && r.Apply[0] == "bluelight-on" {
				on = r.Label
			} else {
				off = r.Label
			}
		}
		if on != "" && off != "" {
			combo.SetSelected(2)
			time.SetText(on + "-" + off)
			a.blueSpec, baseSpec = on+"-"+off, on+"-"+off
		}
		a.updateBlueSub()
		refresh()
	})
	var prof struct {
		Monitors []struct {
			Fields map[string]any `json:"fields"`
		} `json:"monitors"`
	}
	a.call("profile.show", map[string]any{"name": "bluelight-on"}, &prof, func(err error) {
		if err == nil && len(prof.Monitors) > 0 {
			if v, ok := prof.Monitors[0].Fields["bluelight"].(float64); ok && v > 0 {
				baseLevel = int(v) // rebase before SetValue, so no phantom dirty state
				level.SetValue(v)
			}
		}
		refresh()
	})
	return g
}

// buildInput shows inputs as radio rows (one click, current one checked) plus KVM.
// Each row can carry your own name ("Gaming PC") and a global hotkey for jumping to it.
func buildInput(w *window) *adw.PreferencesPage {
	a, b := w.a, w.binder
	f, ok := a.cur.Features["input"]
	if !ok {
		return prefsPage(group(a, b, "KVM", "kvm"))
	}
	g := adw.NewPreferencesGroup()
	g.SetTitle("Input source")
	g.SetDescription("Switching away from this computer's input turns this screen off.")
	var lead *gtk.CheckButton
	checks := map[string]*gtk.CheckButton{}
	reported := a.cur.Caps[fmt.Sprintf("%02x", f.VCP)] // empty = the monitor didn't say; trust the model file
	for i, k := range enumKeys(f) {
		c := gtk.NewCheckButton()
		if lead == nil {
			lead = c
		} else {
			c.SetGroup(lead)
		}
		c.ConnectToggled(func() {
			if c.Active() {
				a.set("input", k)
			}
		})
		checks[k] = c
		r := adw.NewActionRow()
		r.AddPrefix(c)
		r.SetActivatableWidget(c)
		showName := func() {
			r.SetTitle(a.inputLabel(k))
			sub := ""
			if a.inputLabel(k) != valueLabel(k) {
				sub = valueLabel(k)
			}
			r.SetSubtitle(sub)
		}
		showName()
		if len(reported) > 0 && !slices.Contains(reported, f.Values[k]) {
			// dimmed, not disabled: capability strings are often incomplete, so it may still work
			r.AddCSSClass("dim-label")
			r.SetSubtitle(strings.Join(slices.DeleteFunc([]string{r.Subtitle(), "not listed by the monitor"}, func(s string) bool { return s == "" }), " · "))
		}

		rename := gtk.NewButtonFromIconName("document-edit-symbolic")
		rename.AddCSSClass("flat")
		rename.SetVAlign(gtk.AlignCenter)
		rename.SetTooltipText("Give this input your own name")
		setA11yLabel(rename, "Rename "+valueLabel(k))
		rename.ConnectClicked(func() { w.renameInputDialog(k, showName) })
		r.AddSuffix(rename)

		action := "input set " + k
		key := gtk.NewToggleButton()
		key.SetIconName("input-keyboard-symbolic")
		key.AddCSSClass("flat")
		key.SetVAlign(gtk.AlignCenter)
		setA11yLabel(key, "Hotkey for "+valueLabel(k))
		hasKey := func() bool {
			return a.rules != nil && slices.ContainsFunc(a.rules.Shortcuts, func(s ShortcutInfo) bool { return s.Action == action && !s.Default })
		}
		tip := func() {
			key.SetTooltipText(map[bool]string{
				true:  "Hotkey on — click to remove. Assign the keys in System Settings → Shortcuts.",
				false: "Add a global hotkey that switches the default monitor to this input",
			}[key.Active()])
		}
		key.SetActive(hasKey())
		tip()
		key.ConnectToggled(func() {
			if hasKey() == key.Active() {
				return
			}
			method, params := "shortcut.rm", map[string]any{"action": action}
			if key.Active() {
				method, params = "shortcut.add", map[string]any{"action": action, "keys": fmt.Sprintf("Meta+Ctrl+%d", i+1)}
			}
			a.call(method, params, nil, func(err error) {
				if err != nil {
					a.toast(err.Error())
					key.SetActive(hasKey())
					return
				}
				tip()
				a.refreshAutomation()
			})
		})
		r.AddSuffix(key)
		g.Add(r)
	}
	b.on("input", func(fv deck.FeatureValue) {
		if c, ok := checks[fv.Name]; ok {
			c.SetActive(true)
		}
	})
	return prefsPage(g, group(a, b, "KVM", "kvm"))
}

// renameInputDialog sets or clears your name for one input of the current monitor.
func (w *window) renameInputDialog(key string, changed func()) {
	a := w.a
	dlg := adw.NewAlertDialog("Name this input", "Shown instead of “"+valueLabel(key)+"”. Leave empty to go back to the default.")
	dlg.AddResponse("cancel", "Cancel")
	dlg.AddResponse("save", "Save")
	dlg.SetResponseAppearance("save", adw.ResponseSuggested)
	dlg.SetDefaultResponse("save")
	e := gtk.NewEntry()
	e.SetText(a.prefs.InputLabels[a.cur.ID][key])
	e.SetPlaceholderText(valueLabel(key))
	setA11yLabel(e, "Input name")
	e.SetActivatesDefault(true)
	dlg.SetExtraChild(e)
	id := a.cur.ID
	dlg.ConnectResponse(func(answer string) {
		if answer != "save" {
			return
		}
		if a.prefs.InputLabels == nil {
			a.prefs.InputLabels = map[string]map[string]string{}
		}
		if a.prefs.InputLabels[id] == nil {
			a.prefs.InputLabels[id] = map[string]string{}
		}
		if name := strings.TrimSpace(e.Text()); name != "" {
			a.prefs.InputLabels[id][key] = name
		} else {
			delete(a.prefs.InputLabels[id], key)
		}
		a.savePrefs()
		changed()
		w.rebuild() // the dropdown labels on the Display tab follow
		a.refresh()
	})
	dlg.Present(&w.win.Window)
}

func buildMonitor(w *window) *adw.PreferencesPage {
	a := w.a
	m := a.cur
	prop := func(title, value string) *adw.ActionRow {
		r := adw.NewActionRow()
		r.SetTitle(title)
		r.SetSubtitle(value)
		r.SetSubtitleSelectable(true)
		r.AddCSSClass("property")
		return r
	}

	// tier 1: identity, plain words
	info := adw.NewPreferencesGroup()
	info.SetTitle("This monitor")
	info.Add(prop("Model", fmt.Sprintf("%s %s", brand(m.Mfg), m.Model)))
	conn := prop("Connection", connection(m.Connector, m.Bus, a.vals["input"].Name))
	w.binder.on("input", func(fv deck.FeatureValue) {
		if fv.Name != "" {
			conn.SetSubtitle(connection(m.Connector, m.Bus, fv.Name))
		}
	})
	info.Add(conn)

	if len(a.mons) > 1 { // with one monitor there is nothing to choose
		// which monitor the CLI and the rules address when -d is left out (shared config.toml)
		defRow := adw.NewActionRow()
		defRow.SetTitle("Default monitor for CLI and rules")
		pinnedSel := func() string {
			if s := m.Serial; s != "" {
				return s
			}
			return m.ID
		}
		useBtn := gtk.NewButtonWithLabel("Use this monitor")
		useBtn.SetVAlign(gtk.AlignCenter)
		showDefault := func() {
			pinned := deck.DefaultDisplay()
			if deck.Match([]*deck.Monitor{m}, pinned) != nil {
				defRow.SetSubtitle("Pinned to this monitor (" + m.Model + ")")
				useBtn.SetLabel("Unpin")
			} else if pinned != "" {
				defRow.SetSubtitle("Pinned to “" + pinned + "”")
				useBtn.SetLabel("Use this monitor")
			} else {
				defRow.SetSubtitle("Not set — the first monitor detected is used")
				useBtn.SetLabel("Use this monitor")
			}
		}
		useBtn.ConnectClicked(func() {
			toast := m.Model + " is now the default monitor"
			sel := pinnedSel()
			if deck.Match([]*deck.Monitor{m}, deck.DefaultDisplay()) != nil { // pinned here: clear
				sel = ""
				toast = "Default monitor unpinned"
			}
			if err := deck.SetDefaultDisplay(sel); err != nil {
				a.toast(err.Error())
				return
			}
			showDefault()
			a.toast(toast)
		})
		showDefault()
		defRow.AddSuffix(useBtn)
		info.Add(defRow)
	}

	// tier 2: vendor support in one place, with the guided way out
	vendor := adw.NewPreferencesGroup()
	vendor.SetTitle("Vendor features")
	var known []string
	for name := range m.Features {
		if _, std := model.Defaults()[name]; !std {
			known = append(known, title(name))
		}
	}
	sort.Strings(known)
	if m.HasModel && len(known) > 0 {
		vendor.Add(prop("Community file loaded", "Extra features: "+strings.Join(known, ", ")))
		if un := unverifiedTitles(m); len(un) > 0 {
			vendor.Add(unverifiedRow(w, un))
		}
	} else {
		r := adw.NewActionRow()
		r.SetTitle("No community file for this model")
		r.SetSubtitle("Standard features only. Look it up in the community database, or map it yourself under Advanced.")
		id := fmt.Sprintf("%s-%s-%d", m.Mfg, m.Model, m.Product)
		fetch := gtk.NewButtonWithLabel("Look up")
		fetch.SetVAlign(gtk.AlignCenter)
		fetch.SetTooltipText(fmt.Sprintf("Sends only the model ID (%s) to github.com. Nothing else.", id))
		fetch.ConnectClicked(func() {
			fetch.SetSensitive(false)
			a.call("model.fetch", map[string]any{"id": id}, nil, func(err error) {
				fetch.SetSensitive(true)
				if err != nil {
					a.toast(err.Error()) // usually: none in the database yet
					return
				}
				a.toast("Model file loaded")
				var res deck.ListResult
				a.call("list", deck.Params{}, &res, func(error) { a.setMonitors(res.Monitors) })
			})
		})
		r.AddSuffix(fetch)
		vendor.Add(r)
	}
	if missing := missingTitles(m); len(missing) > 0 {
		vendor.Add(prop("Not available on this monitor", strings.Join(missing, ", ")))
	}
	disco := adw.NewActionRow()
	disco.SetTitle("Map more features")
	disco.SetSubtitle("Change a setting in the monitor's menu; DDC Deck finds the code behind it.")
	start := gtk.NewButtonWithLabel("Discover…")
	start.SetTooltipText("Guided discovery of vendor VCP codes by diffing OSD changes")
	start.SetVAlign(gtk.AlignCenter)
	start.ConnectClicked(func() { a.openWizard() })
	disco.AddSuffix(start)

	// tier 3: jargon behind one collapsed expander
	adv := adw.NewExpanderRow()
	adv.SetTitle("Advanced")
	adv.SetSubtitle("Map features, raw VCP codes, factory reset")
	adv.AddRow(disco)
	adv.AddRow(prop("Serial number", m.Serial))
	adv.AddRow(prop("Product code", strconv.Itoa(m.Product)))
	adv.AddRow(prop("Model file", fmt.Sprintf("%s (data/models/%s-%s-%d.toml)", map[bool]string{true: "loaded", false: "none"}[m.HasModel], m.Mfg, m.Model, m.Product)))
	for _, r := range unmappedRows(a) {
		adv.AddRow(r)
	}
	for _, r := range rawVCPRows(w) {
		adv.AddRow(r)
	}
	if _, supported := m.Features["reset"]; supported { // last and apart: never beside the safe actions
		rst := adw.NewActionRow()
		rst.SetTitle("Factory reset " + m.Model)
		rst.SetSubtitle("Every monitor setting back to its defaults; profiles are not affected")
		btn := gtk.NewButtonWithLabel("Reset…")
		btn.AddCSSClass("destructive-action")
		btn.SetVAlign(gtk.AlignCenter)
		btn.ConnectClicked(func() {
			confirm := adw.NewAlertDialog(fmt.Sprintf("Reset %s (%s)?", m.Model, m.Connector), "Brightness, color and input settings on this monitor go back to factory defaults.")
			confirm.AddResponse("cancel", "Cancel")
			confirm.AddResponse("reset", "Reset")
			confirm.SetResponseAppearance("reset", adw.ResponseDestructive)
			confirm.SetDefaultResponse("cancel")
			confirm.ConnectResponse(func(answer string) {
				if answer != "reset" {
					return
				}
				a.set("reset", "factory")
				a.toast("Factory reset sent to " + m.Model)
				a.refresh() // every value just changed on the monitor itself
			})
			confirm.Present(&w.win.Window)
		})
		rst.AddSuffix(btn)
		adv.AddRow(rst)
	}
	advanced := adw.NewPreferencesGroup()
	advanced.Add(adv)

	groups := []*adw.PreferencesGroup{info, vendor, advanced}
	return prefsPage(groups...)
}

// diagSummary headlines the checklist so the state is obvious without counting icons.
func diagSummary(bad, n int) string {
	if bad == 0 {
		return fmt.Sprintf("All %d checks passed", n)
	}
	return fmt.Sprintf("%d of %d checks need attention — look for the yellow and red rows", bad, n)
}

// diagCheck mirrors one doctor result row.
type diagCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// buildDiagnostics is the GUI's `ddcdeck doctor`: every link in the chain, green or not.
// The verdict sits on top as its own row; "Copy report" puts the whole list on the clipboard
// for bug reports.
func buildDiagnostics(w *window) *adw.PreferencesGroup {
	a := w.a
	g := adw.NewPreferencesGroup()
	g.SetTitle("Diagnostics")
	recheck := gtk.NewButtonFromIconName("view-refresh-symbolic")
	recheck.AddCSSClass("flat")
	recheck.SetVAlign(gtk.AlignCenter)
	recheck.SetTooltipText("Run the checks again")
	setA11yLabel(recheck, "Run the checks again")
	var cs []diagCheck
	copyBtn := gtk.NewButtonFromIconName("edit-copy-symbolic")
	copyBtn.AddCSSClass("flat")
	copyBtn.SetVAlign(gtk.AlignCenter)
	copyBtn.SetTooltipText("Copy the report for a bug report")
	setA11yLabel(copyBtn, "Copy the report")
	copyBtn.SetSensitive(false)
	copyBtn.ConnectClicked(func() {
		var sb strings.Builder
		fmt.Fprintf(&sb, "DDC Deck %s\n%s\n", appVersion(), diagSummary(countBad(cs), len(cs)))
		for _, c := range cs {
			fmt.Fprintf(&sb, "[%s] %s: %s\n", c.State, c.Name, c.Detail)
		}
		w.win.Clipboard().SetText(sb.String())
		a.toast("Report copied")
	})
	rows := []*adw.ActionRow{}
	run := func() {
		var res []diagCheck
		a.call("doctor", map[string]any{}, &res, func(err error) {
			if err != nil {
				return // old daemon without the method
			}
			cs = res
			for _, r := range rows {
				g.Remove(r)
			}
			rows = rows[:0]
			bad := countBad(cs)
			copyBtn.SetSensitive(true)
			sum := adw.NewActionRow()
			sum.SetTitle("<b>" + html.EscapeString(diagSummary(bad, len(cs))) + "</b>")
			sum.AddPrefix(gtk.NewImageFromIconName(map[bool]string{true: "object-select-symbolic", false: "dialog-warning-symbolic"}[bad == 0]))
			rows = append(rows, sum)
			g.Add(sum)
			for _, c := range cs {
				row := adw.NewActionRow()
				row.SetTitle(c.Name)
				row.SetSubtitle(html.EscapeString(c.Detail)) // subtitles are parsed as Pango markup
				icon := map[string]string{"ok": "object-select-symbolic", "warn": "dialog-warning-symbolic"}[c.State]
				if icon == "" {
					icon = "dialog-error-symbolic" // fail
				}
				row.AddPrefix(gtk.NewImageFromIconName(icon))
				rows = append(rows, row)
				g.Add(row)
			}
		})
	}
	recheck.ConnectClicked(run)
	hdr := gtk.NewBox(gtk.OrientationHorizontal, 0)
	hdr.Append(copyBtn)
	hdr.Append(recheck)
	g.SetHeaderSuffix(hdr)
	run()
	return g
}

func countBad(cs []diagCheck) (bad int) {
	for _, c := range cs {
		if c.State != "ok" {
			bad++
		}
	}
	return
}

func missingTitles(m *deck.Monitor) []string {
	var missing []string
	for _, name := range placed {
		if _, ok := m.Features[name]; !ok {
			missing = append(missing, title(name))
		}
	}
	return missing
}

// brand turns an EDID PNP id into the vendor name users know.
func brand(mfg string) string {
	names := map[string]string{
		"AUS": "ASUS", "SAM": "Samsung", "DEL": "Dell", "BNQ": "BenQ", "GSM": "LG",
		"HPN": "HP", "IVM": "Iiyama", "AOC": "AOC", "PHL": "Philips", "MED": "Medion",
		"ACR": "Acer", "LEN": "Lenovo", "MSI": "MSI", "GWB": "Gateway", "NEC": "NEC",
	}
	if n, ok := names[strings.ToUpper(mfg)]; ok {
		return n
	}
	return mfg
}

// connection names the physical cable. DRM connectors can't distinguish USB-C
// (DisplayPort alt mode) from a native DP port — but the monitor knows which input
// it is receiving, so prefer that; fall back to the connector name.
func connection(conn string, bus int, input string) string {
	if input == "usbc" {
		return fmt.Sprintf("USB-C (%s), bus %d", conn, bus)
	}
	base := conn
	if i := strings.IndexByte(conn, '-'); i > 0 {
		base = conn[:i]
	}
	kind := map[string]string{
		"DP": "DisplayPort", "HDMI": "HDMI", "HDMI-A": "HDMI", "HDMI-B": "HDMI",
		"eDP": "Built-in display", "DVI": "DVI", "VGA": "VGA",
	}[base]
	if strings.HasPrefix(input, "hdmi") {
		kind = "HDMI"
	} else if strings.HasPrefix(input, "dp") {
		kind = "DisplayPort"
	}
	if kind == "" {
		kind = base
	}
	return fmt.Sprintf("%s (%s), bus %d", kind, conn, bus)
}

// unmapped lists codes the monitor reports but no feature maps, named where ddcutil's
// MCCS table knows them (queried at runtime: the table is GPL, we don't copy it).

// unmappedRows lists codes the monitor reports but no feature maps, named where
// ddcutil's MCCS table knows them (queried at runtime: the table is GPL, we don't copy it).
func unmappedRows(a *App) []*adw.ActionRow {
	if a.cur == nil || len(a.cur.Caps) == 0 {
		return nil
	}
	used := map[byte]bool{}
	for _, f := range a.cur.Features {
		used[byte(f.VCP)] = true
	}
	names := vcpinfoNames()
	var rows []*adw.ActionRow
	for hex, values := range a.cur.Caps {
		c, err := strconv.ParseUint(hex, 16, 8)
		if err != nil || used[byte(c)] {
			continue
		}
		name := names[byte(c)]
		if name == "" || strings.Contains(strings.ToLower(name), "reserved") {
			name = "manufacturer-specific"
		}
		r := adw.NewActionRow()
		r.SetTitle(fmt.Sprintf("VCP %02X — %s", c, name))
		r.SetTitleSelectable(true)
		if len(values) > 0 {
			vs := make([]string, len(values))
			for i, v := range values {
				vs[i] = fmt.Sprintf("%02X", v)
			}
			r.SetSubtitle("values: " + strings.Join(vs, " "))
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Title() < rows[j].Title() })
	return rows
}

// rawVCPRows is the escape hatch: read or write any VCP code.
func rawVCPRows(w *window) []gtk.Widgetter {
	a := w.a
	code := adw.NewEntryRow()
	code.SetTitle("Code (hex, e.g. E6)")
	val := adw.NewEntryRow()
	val.SetTitle("Value (decimal or 0x…)")
	result := adw.NewActionRow()
	result.SetTitle("Result")
	result.SetSubtitle("—")
	result.SetSubtitleSelectable(true)
	read := gtk.NewButtonWithLabel("Read")
	write := gtk.NewButtonWithLabel("Write")
	write.AddCSSClass("destructive-action")
	for _, btn := range []*gtk.Button{read, write} {
		btn.SetVAlign(gtk.AlignCenter)
		result.AddSuffix(btn)
	}
	read.ConnectClicked(func() {
		var v ddc.Value
		a.call("vcp.get", deck.Params{Code: code.Text()}, &v, func(err error) {
			if err != nil {
				result.SetSubtitle(err.Error())
				return
			}
			result.SetSubtitle(fmt.Sprintf("%d (0x%02x), max %d", v.Cur, v.Cur, v.Max))
		})
	})
	write.ConnectClicked(func() {
		raw, _ := json.Marshal(val.Text())
		a.call("vcp.set", deck.Params{Code: code.Text(), Value: raw}, nil, func(err error) {
			if err != nil {
				result.SetSubtitle(err.Error())
				return
			}
			result.SetSubtitle("Wrote " + val.Text() + " to " + strings.ToUpper(code.Text()))
			a.refresh()
		})
	})
	return []gtk.Widgetter{code, val, result}
}

var vcpinfoOnce sync.Once
var vcpinfoMap map[byte]string

func vcpinfoNames() map[byte]string {
	vcpinfoOnce.Do(func() {
		vcpinfoMap = map[byte]string{}
		out, err := exec.Command("ddcutil", "vcpinfo", "--verbose").Output()
		if err != nil {
			return
		}
		for _, m := range regexp.MustCompile(`(?m)^VCP code ([0-9a-fA-F]{2}): (.+)$`).FindAllStringSubmatch(string(out), -1) {
			c, _ := strconv.ParseUint(m[1], 16, 8)
			vcpinfoMap[byte(c)] = strings.TrimSpace(m[2])
		}
	})
	return vcpinfoMap
}
