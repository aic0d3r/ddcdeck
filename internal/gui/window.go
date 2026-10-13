package gui

import (
	"encoding/json"
	"fmt"
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
		{"settings", "Settings", "emblem-system-symbolic", buildSettings},
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
	hb.PackEnd(autoBtn)
	w.stack = gtk.NewStack()
	w.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.status = adw.NewStatusPage()
	w.status.SetIconName("video-display-symbolic")
	w.stack.AddNamed(w.status, "status")
	w.banner = adw.NewBanner("This monitor works, but its special features need one guided pass")
	w.banner.SetButtonLabel("Discover features…")
	w.banner.ConnectButtonClicked(func() { a.openWizard() })
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
	w.banner.SetRevealed(!a.cur.HasModel && w.stack.VisibleChildName() != "settings")

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
		w.banner.SetRevealed(name != "settings" && !w.a.cur.HasModel)
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
		if !slices.Contains(placed, name) {
			more = append(more, name)
		}
	}
	slices.Sort(more)
	return prefsPage(
		group(a, b, "Picture", "brightness", "contrast", "sharpness"),
		group(a, b, "Color", "preset", "colortemp"),
		group(a, b, "Eye care", "bluelight"),
		group(a, b, "Audio", "volume", "mute"),
		group(a, b, "Power", "power"),
		group(a, b, "More", more...),
	)
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

func buildSettings(w *window) *adw.PreferencesPage {
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
	} else {
		vendor.Add(prop("No community file for this model", "Standard features only — discover the rest below."))
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

	return prefsPage(info, vendor, advanced)
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
