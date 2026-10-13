package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
	"github.com/aic0d3r/ddcdeck/internal/wizard"
)

// guiWizard drives the OSD-diffing wizard (internal/wizard) from an AdwDialog.
// One fixed layout — heading, instruction, body, reserved feedback row, buttons —
// whose content changes per phase, so feedback never moves the layout.
type guiWizard struct {
	a    *App
	dlg  *adw.Dialog
	mon  deck.Monitor
	sess *wizard.Session

	step        *gtk.Label // "Step 2 of 4"
	title       *gtk.Label
	desc        *gtk.Label
	body        *gtk.Box
	spinner     *gtk.Spinner
	busy        *gtk.Label
	primary     *gtk.Button
	secondary   *gtk.Button
	onPrimary   func()
	onSecondary func()

	name    string
	round   int
	prev    map[byte]ddc.Value
	diff    map[byte][2]ddc.Value
	values  map[string]int
	curVal  int
	off     *int
	finding wizard.Finding
}

var wizardName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var suggestions = []string{"bluelight", "kvm", "preset", "gamemode", "sharpness", "crosshair", "hud", "pip"}

// openWizard starts the discovery dialog for the current monitor.
func (a *App) openWizard() {
	if a.cur == nil {
		return
	}
	var m deck.Monitor
	a.call("monitor", deck.Params{}, &m, func(err error) {
		if err != nil {
			a.toast(err.Error())
			return
		}
		w := &guiWizard{a: a, mon: m, values: map[string]int{}}
		w.build()
		w.phaseName()
		w.dlg.Present(&a.win.win.Window)
	})
}

func (w *guiWizard) build() {
	w.dlg = adw.NewDialog()
	w.dlg.SetTitle("Discover features")
	w.dlg.SetContentWidth(460)

	root := gtk.NewBox(gtk.OrientationVertical, 4)
	root.SetMarginTop(16)
	root.SetMarginBottom(12)
	root.SetMarginStart(16)
	root.SetMarginEnd(16)

	head := gtk.NewBox(gtk.OrientationHorizontal, 0)
	w.title = gtk.NewLabel("")
	w.title.AddCSSClass("heading")
	w.title.SetHExpand(true)
	w.title.SetXAlign(0)
	w.title.SetWrap(true)
	w.step = gtk.NewLabel("")
	w.step.AddCSSClass("dim-label")
	w.step.SetVAlign(gtk.AlignStart)
	head.Append(w.title)
	head.Append(w.step)
	root.Append(head)

	w.desc = gtk.NewLabel("")
	w.desc.SetXAlign(0)
	w.desc.SetWrap(true)
	root.Append(w.desc)

	w.body = gtk.NewBox(gtk.OrientationVertical, 8)
	root.Append(w.body)

	// reserved feedback row: spinner and text never appear or vanish, only animate
	feedback := gtk.NewBox(gtk.OrientationHorizontal, 8)
	feedback.SetMarginTop(4)
	feedback.SetMarginBottom(4)
	w.spinner = gtk.NewSpinner()
	w.spinner.SetSpinning(false)
	setA11yLabel(w.spinner, "Working")
	feedback.Append(w.spinner)
	w.busy = gtk.NewLabel("")
	w.busy.SetXAlign(0)
	w.busy.AddCSSClass("dim-label")
	feedback.Append(w.busy)
	root.Append(feedback)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { w.dlg.Close() })
	buttons.Append(cancel)
	buttons.Append(gtk.NewBox(gtk.OrientationHorizontal, 0)) // spacer
	w.secondary = gtk.NewButtonWithLabel("")
	w.secondary.SetVisible(false)
	w.secondary.ConnectClicked(func() {
		if w.onSecondary != nil {
			w.onSecondary()
		}
	})
	buttons.Append(w.secondary)
	w.primary = gtk.NewButtonWithLabel("")
	w.primary.AddCSSClass("suggested-action")
	w.primary.ConnectClicked(func() {
		if w.onPrimary != nil {
			w.primary.SetSensitive(false)
			w.onPrimary()
		}
	})
	buttons.Append(w.primary)
	root.Append(buttons)

	w.dlg.SetChild(root)
}

// setPhase renders one phase: heading, instruction, body controls and buttons.
func (w *guiWizard) setPhase(step, of int, title, desc string, body func(b *gtk.Box), primary, secondary string, onPrimary, onSecondary func()) {
	w.step.SetText(fmt.Sprintf("Step %d of %d", step, of))
	w.title.SetText(title)
	w.desc.SetText(desc)
	for w.body.FirstChild() != nil {
		w.body.Remove(w.body.FirstChild())
	}
	if body != nil {
		body(w.body)
	}
	w.setBusy("")
	w.onPrimary, w.onSecondary = onPrimary, onSecondary
	if primary == "" {
		w.primary.SetVisible(false)
	} else {
		w.primary.SetVisible(true)
		w.primary.SetLabel(primary)
		w.primary.SetSensitive(true)
	}
	if secondary == "" {
		w.secondary.SetVisible(false)
	} else {
		w.secondary.SetVisible(true)
		w.secondary.SetLabel(secondary)
	}
}

func (w *guiWizard) setBusy(msg string) {
	w.busy.SetText(msg)
	w.spinner.SetSpinning(msg != "")
	if msg == "" {
		w.primary.SetSensitive(true)
	}
}

// goDDC runs f off the main loop; UI stays responsive, buttons stay disabled.
func (w *guiWizard) goDDC(busy string, f func() error, done func(err error)) {
	w.setBusy(busy)
	go func() {
		err := f()
		glib.IdleAdd(func() {
			w.setBusy("")
			done(err)
		})
	}()
}

func (w *guiWizard) phaseName() {
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("e.g. bluelight")
	setA11yLabel(entry, "Feature name")
	quick := gtk.NewDropDownFromStrings(suggestions)
	quick.SetTooltipText("Fill in a suggestion")
	quick.Connect("notify::selected", func() {
		if i := int(quick.Selected()); i < len(suggestions) {
			entry.SetText(suggestions[i])
		}
	})
	w.setPhase(1, 4, "Which feature?",
		fmt.Sprintf("Name the %s setting you want to map. You will change it in the monitor's OSD and DDC Deck will find the VCP code behind it.", w.mon.Model),
		func(b *gtk.Box) {
			b.Append(entry)
			b.Append(quick)
		},
		"Start", "", func() {
			name := strings.ToLower(strings.TrimSpace(entry.Text()))
			if !wizardName.MatchString(name) {
				w.a.toast("Names are lowercase letters, digits and underscores")
				w.primary.SetSensitive(true)
				return
			}
			w.name = name
			w.goDDC("Reading the monitor's codes twice (about 40 s)…", func() error {
				s, err := wizard.Start(ddc.Ddcutil{}, w.mon.Bus)
				if err == nil {
					w.sess = s
				}
				return err
			}, func(err error) {
				if err != nil {
					w.a.toast(err.Error())
					w.primary.SetSensitive(true)
					return
				}
				w.round, w.diff, w.values = 0, nil, map[string]int{}
				w.prev = w.sess.Base()
				w.phaseOsd("")
			})
		}, nil)
	w.dlg.SetFocus(entry)
}

func (w *guiWizard) phaseOsd(note string) {
	instruction := fmt.Sprintf("In the monitor's OSD, change “%s”, then press Read codes.", w.name)
	if note != "" {
		instruction = note + "\n" + instruction
	}
	w.setPhase(2, 4, "Change it in the OSD", instruction, nil,
		"Read codes", "", func() {
			var snap map[byte]ddc.Value
			w.goDDC(fmt.Sprintf("Reading %d codes…", len(w.sess.Codes())), func() (err error) {
				snap, err = w.sess.Snapshot()
				return err
			}, func(err error) { w.afterRead(err, snap) })
		}, nil)
}

// afterRead applies the diff logic to one finished snapshot.
func (w *guiWizard) afterRead(err error, snap map[byte]ddc.Value) {
	if err != nil {
		w.a.toast(err.Error())
		w.phaseOsd("Reading failed — try again.")
		return
	}
	d := wizard.Changed(w.prev, snap)
	if w.round > 0 {
		d = wizard.Intersect(w.diff, d)
		for c, v := range d {
			d[c] = [2]ddc.Value{w.diff[c][0], v[1]}
		}
	}
	w.prev, w.diff = snap, d
	switch len(d) {
	case 1:
		var code byte
		var pair [2]ddc.Value
		for c, v := range d {
			code, pair = c, v
		}
		w.finding = wizard.Finding{Code: code, Old: pair[0], New: pair[1]}
		if w.finding.New.NC {
			w.curVal = w.finding.New.Cur
			w.phaseValue("")
		} else {
			w.phaseOff()
		}
	case 0:
		w.phaseOsd("No VCP code changed. Make sure you apply the setting in the monitor's menu.")
	default:
		w.round++
		w.phaseOsd(fmt.Sprintf("%d codes changed (%s) — ambiguous.", len(d), w.codes()))
	}
}

func (w *guiWizard) codes() string {
	var cs []string
	for c := range w.diff {
		cs = append(cs, fmt.Sprintf("%02X", c))
	}
	return strings.Join(cs, " ")
}

// phaseValue names the enum value just set (0x0F → "user1", …).
func (w *guiWizard) phaseValue(note string) {
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("e.g. hdmi1")
	setA11yLabel(entry, "Value name")
	collected := gtk.NewLabel(w.valueList())
	collected.AddCSSClass("dim-label")
	collected.SetXAlign(0)
	collected.SetWrap(true)
	w.setPhase(3, 4, "Name that setting",
		note+fmt.Sprintf("“%s” is now at 0x%02X. Give this value a name.", w.name, w.curVal),
		func(b *gtk.Box) {
			b.Append(entry)
			b.Append(collected)
		},
		"Save value", "Finish", func() {
			n := strings.ToLower(strings.TrimSpace(entry.Text()))
			if !wizardName.MatchString(n) {
				w.a.toast("Names are lowercase letters, digits and underscores")
				return
			}
			w.values[n] = w.curVal
			w.phaseOsdValue()
		}, func() {
			if len(w.values) == 0 {
				w.a.toast("Name at least one value")
				return
			}
			w.phaseConfirm()
		})
	w.dlg.SetFocus(entry)
}

func (w *guiWizard) valueList() string {
	if len(w.values) == 0 {
		return "No values named yet."
	}
	var ps []string
	for _, k := range sortedKeys(w.values) {
		ps = append(ps, fmt.Sprintf("%s=0x%02X", k, w.values[k]))
	}
	return "Collected: " + strings.Join(ps, ", ")
}

// phaseOsdValue is the value-collection variant of the OSD phase.
func (w *guiWizard) phaseOsdValue() {
	w.setPhase(3, 4, "Another value?", w.valueList()+"\nSet “"+w.name+"” to another setting in the OSD, then press Read codes — or finish.", nil,
		"Read codes", "Finish", func() {
			w.goDDC("Reading…", func() error {
				snap, err := w.sess.Snapshot()
				if err == nil {
					w.curVal = snap[w.finding.Code].Cur
				}
				return err
			}, func(err error) {
				if err != nil {
					w.a.toast(err.Error())
					return
				}
				w.phaseValue("")
			})
		}, w.phaseConfirm)
}

// phaseOff asks for the optional off value of a range feature.
func (w *guiWizard) phaseOff() {
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("blank if there is no off value")
	setA11yLabel(entry, "Off value")
	w.setPhase(3, 4, fmt.Sprintf("Found VCP %02X", w.finding.Code),
		fmt.Sprintf("“%s” is a range up to %d. Which value means “off”?", w.name, w.finding.New.Max),
		func(b *gtk.Box) { b.Append(entry) },
		"Continue", "", func() {
			if t := strings.TrimSpace(entry.Text()); t != "" {
				n, err := strconv.Atoi(t)
				if err != nil {
					w.a.toast("The off value is a number")
					return
				}
				w.off = &n
			}
			w.phaseConfirm()
		}, nil)
	w.dlg.SetFocus(entry)
}

func (w *guiWizard) phaseConfirm() {
	w.setPhase(4, 4, "Check the monitor",
		fmt.Sprintf("Writing the old value (%d) back through DDC…", w.finding.Old.Cur), nil,
		"", "", nil, nil)
	w.goDDC("Writing…", func() error { return w.sess.Set(w.finding.Code, w.finding.Old.Cur) }, func(err error) {
		if err != nil {
			w.a.toast(err.Error())
		}
		w.setPhase(4, 4, "Check the monitor",
			fmt.Sprintf("The old value (%d) was written back. Does the monitor's OSD show “%s” at %d again?", w.finding.Old.Cur, w.name, w.finding.Old.Cur), nil,
			"Yes, save it", "No", func() {
				w.save()
			}, func() {
				w.a.toast("Not saved — the code is probably unrelated")
				w.phaseName()
			})
	})
}

func (w *guiWizard) save() {
	feat := w.finding.Feature(w.off, w.values)
	disco := model.Model{Mfg: w.mon.Mfg, Model: w.mon.Model, Product: w.mon.Product,
		Features: map[string]model.Feature{w.name: feat}}
	modelPath, err := model.SaveUserModel(disco)
	if err != nil {
		w.a.toast(err.Error())
		w.phaseName()
		return
	}
	mccsPath := filepath.Join(model.DdcutilDir(), fmt.Sprintf("%s-%s-%d.mccs", w.mon.Mfg, w.mon.Model, w.mon.Product))
	if err := os.MkdirAll(model.DdcutilDir(), 0o700); err == nil {
		_ = os.WriteFile(mccsPath, []byte(disco.MCCS()), 0o600)
	}

	// let the daemon rescan so the new feature appears immediately
	w.a.call("scan", deck.Params{}, nil, func(error) {
		var res deck.ListResult
		w.a.call("list", deck.Params{}, &res, func(error) {
			w.a.setMonitors(res.Monitors)
		})
	})

	copyBtn := gtk.NewButtonWithLabel("Copy for GitHub PR")
	copyBtn.ConnectClicked(func() {
		gdk.DisplayGetDefault().Clipboard().SetText(w.prText(disco, modelPath))
		w.a.toast("Copied — paste it into a GitHub issue or PR")
	})
	another := gtk.NewButtonWithLabel("Discover another")
	another.ConnectClicked(func() {
		w.round, w.diff, w.values = 0, nil, map[string]int{}
		w.prev = w.sess.Base()
		w.phaseName()
	})
	w.setPhase(4, 4, fmt.Sprintf("%s saved", w.name),
		fmt.Sprintf("The feature works right away: try “%s” on the Display page or `ddcdeck %s get`.\nModel file: %s", w.name, w.name, modelPath),
		func(b *gtk.Box) {
			row := gtk.NewBox(gtk.OrientationHorizontal, 8)
			row.Append(copyBtn)
			row.Append(another)
			b.Append(row)
		},
		"Close", "", func() { w.dlg.Close() }, nil)
}

func (w *guiWizard) prText(m model.Model, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Feature `%s` for %s %s (product %d), found with `ddcdeck discover`.\n\n", w.name, w.mon.Mfg, w.mon.Model, w.mon.Product)
	if b2, err := os.ReadFile(path); err == nil {
		b.Write(b2)
	}
	b.WriteString("\n`ddcdeck model export-mccs` regenerates the ddcutil file from it.\n")
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j] < ks[j-1]; j-- {
			ks[j], ks[j-1] = ks[j-1], ks[j]
		}
	}
	return ks
}
