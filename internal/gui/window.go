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
	binder    *binder
}

func newWindow(a *App) *window {
	w := &window{a: a, binder: &binder{}}
	w.win = adw.NewApplicationWindow(&a.app.Application)
	w.win.SetTitle("DDC Deck")
	w.win.AddCSSClass("ddcdeck-main")
	w.win.SetDefaultSize(820, 600)
	w.win.SetHideOnClose(true) // close = back to the tray
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
	sideTV := adw.NewToolbarView()
	sideTV.AddTopBar(adw.NewHeaderBar())
	sideTV.SetContent(w.sidebar)
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
	w.sidebar.SetSensitive(false)
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
	w.sidebar.SetSensitive(true)
	if w.bannerFor != a.cur.ID {
		w.bannerFor, w.notFound = a.cur.ID, false
	}
	w.setBanner()
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
		group(a, b, "Picture", "brightness", "contrast", "sharpness"),
		group(a, b, "Color", "preset", "colortemp"),
		buildEyeCare(w),
		group(a, b, "Audio", "volume", "mute"),
		group(a, b, "Power", "power"),
		group(a, b, "More", more...),
	)
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
	for _, k := range enumKeys(f) {
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
		r.SetTitle(valueLabel(k))
		r.AddPrefix(c)
		r.SetActivatableWidget(c)
		g.Add(r)
	}
	b.on("input", func(fv deck.FeatureValue) {
		if c, ok := checks[fv.Name]; ok {
			c.SetActive(true)
		}
	})
	return prefsPage(g, group(a, b, "KVM", "kvm"))
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
		r.SetSubtitle("Standard features only. Look it up in the community database, or map the rest below.")
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
	vendor.Add(disco)

	// tier 3: jargon behind one collapsed expander
	adv := adw.NewExpanderRow()
	adv.SetTitle("Advanced")
	adv.SetSubtitle("Raw VCP codes — power-user territory")
	adv.AddRow(prop("Serial number", m.Serial))
	adv.AddRow(prop("Product code", strconv.Itoa(m.Product)))
	adv.AddRow(prop("Model file", fmt.Sprintf("%s (data/models/%s-%s-%d.toml)", map[bool]string{true: "loaded", false: "none"}[m.HasModel], m.Mfg, m.Model, m.Product)))
	for _, r := range unmappedRows(a) {
		adv.AddRow(r)
	}
	for _, r := range rawVCPRows(w) {
		adv.AddRow(r)
	}
	advanced := adw.NewPreferencesGroup()
	advanced.Add(adv)

	groups := []*adw.PreferencesGroup{info, vendor, advanced}
	if _, supported := m.Features["reset"]; supported { // last and apart: never beside the safe actions
		resetGroup := adw.NewPreferencesGroup()
		resetGroup.SetTitle("Reset")
		rst := adw.NewActionRow()
		rst.SetTitle("Factory reset")
		rst.SetSubtitle("Every monitor setting back to its defaults; profiles are not affected")
		btn := gtk.NewButtonWithLabel("Reset…")
		btn.AddCSSClass("destructive-action")
		btn.SetVAlign(gtk.AlignCenter)
		btn.ConnectClicked(func() {
			confirm := adw.NewAlertDialog("Reset this monitor?", "Brightness, color and input settings go back to factory defaults.")
			confirm.AddResponse("cancel", "Cancel")
			confirm.AddResponse("reset", "Reset")
			confirm.SetResponseAppearance("reset", adw.ResponseDestructive)
			confirm.SetDefaultResponse("cancel")
			confirm.ConnectResponse(func(answer string) {
				if answer != "reset" {
					return
				}
				a.set("reset", "factory")
				a.toast("Factory reset sent")
				a.refresh() // every value just changed on the monitor itself
			})
			confirm.Present(&w.win.Window)
		})
		rst.AddSuffix(btn)
		resetGroup.Add(rst)
		groups = append(groups, resetGroup)
	}
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
func buildDiagnostics(w *window) *adw.PreferencesGroup {
	a := w.a
	g := adw.NewPreferencesGroup()
	g.SetTitle("Diagnostics")
	recheck := gtk.NewButtonFromIconName("view-refresh-symbolic")
	recheck.SetVAlign(gtk.AlignCenter)
	recheck.SetTooltipText("Run the checks again")
	rows := []*adw.ActionRow{}
	run := func() {
		var cs []diagCheck
		a.call("doctor", map[string]any{}, &cs, func(err error) {
			if err != nil {
				return // old daemon without the method
			}
			for _, r := range rows {
				g.Remove(r)
			}
			rows = rows[:0]
			bad := 0
			for _, c := range cs {
				if c.State != "ok" {
					bad++
				}
			}
			g.SetDescription(diagSummary(bad, len(cs)))
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
	g.SetHeaderSuffix(recheck)
	run()
	return g
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
