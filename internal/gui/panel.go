package gui

import (
	"time"

	"github.com/diamondburned/gotk4-layer-shell/pkg/gtk4layershell"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const (
	panelWidth  = 380
	panelMargin = 12
	settleTime  = 500 * time.Millisecond // KDE drops focus spuriously right after mapping
)

// panel is the tray quick panel. It is a fresh layer-shell surface each time it opens and is
// destroyed on close: re-mapping a hidden layer surface leaves ghost artifacts on KWin.
type panel struct {
	a       *App
	win     *gtk.Window
	binder  *binder
	opened  time.Time
	pointer bool
	closed  bool
}

func (a *App) togglePanelAt(x, y int) {
	if a.panel != nil {
		a.panel.close()
		return
	}
	if a.cur == nil { // nothing to control: show why in the main window
		a.showWindow()
		return
	}
	a.panel = newPanel(a, x, y)
	a.refreshFresh() // the panel is a one-shot surface: it should show the monitor's real state
}

func newPanel(a *App, x, y int) *panel {
	p := &panel{a: a, binder: &binder{}, opened: time.Now()}
	p.win = gtk.NewWindow()
	p.win.SetApplication(&a.app.Application)
	p.win.SetTitle("DDC Deck quick settings")
	p.win.SetDefaultSize(panelWidth, -1)
	p.win.SetResizable(false)
	if gtk4layershell.IsSupported() {
		gtk4layershell.InitForWindow(p.win)
		gtk4layershell.SetNamespace(p.win, "ddcdeck")
		gtk4layershell.SetLayer(p.win, gtk4layershell.LayerShellLayerTop)
		placePanel(p.win, x, y)
		gtk4layershell.SetKeyboardMode(p.win, gtk4layershell.LayerShellKeyboardModeOnDemand)
	} else {
		p.win.SetDecorated(false)
	}
	p.win.AddCSSClass("ddcdeck-panel")

	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.AddCSSClass("ddcdeck-panel-box")

	head := gtk.NewBox(gtk.OrientationHorizontal, 6)
	name := gtk.NewLabel(a.cur.Model)
	name.AddCSSClass("heading")
	name.SetHExpand(true)
	name.SetXAlign(0)
	head.Append(name)
	open := gtk.NewButtonFromIconName("view-more-horizontal-symbolic")
	open.SetTooltipText("Open DDC Deck")
	setA11yLabel(open, "Open DDC Deck")
	open.AddCSSClass("flat")
	open.ConnectClicked(func() { p.close(); a.showWindow() })
	head.Append(open)
	box.Append(head)

	list := gtk.NewListBox()
	list.AddCSSClass("boxed-list")
	list.SetSelectionMode(gtk.SelectionNone)
	for _, feature := range []string{"brightness", "bluelight", "preset", "colortemp", "input"} {
		f, ok := a.cur.Features[feature]
		if !ok {
			continue
		}
		rows := featureRows(a, p.binder, feature, f)
		if feature == "bluelight" {
			rows = rows[:1] // just the switch; the level lives in the main window
		}
		for _, r := range rows {
			list.Append(r)
		}
	}
	box.Append(list)
	p.win.SetChild(box)
	a.addBinder(p.binder)
	for _, fv := range a.vals { // show cached values immediately; refresh follows
		a.apply(fv)
	}

	// dismiss: Escape, or focus loss with the pointer outside (ignores KDE's spurious drops)
	key := gtk.NewEventControllerKey()
	key.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if keyval == gdk.KEY_Escape {
			p.close()
			return true
		}
		return false
	})
	p.win.AddController(key)
	motion := gtk.NewEventControllerMotion()
	motion.ConnectEnter(func(_, _ float64) { p.pointer = true })
	motion.ConnectLeave(func() { p.pointer = false })
	p.win.AddController(motion)
	p.win.Connect("notify::is-active", func() {
		if p.win.IsActive() || time.Since(p.opened) < settleTime || p.pointer {
			return
		}
		glib.TimeoutAdd(200, func() bool {
			if !p.win.IsActive() {
				p.close()
			}
			return false
		})
	})
	p.win.Present()
	return p
}

func (p *panel) close() {
	if p.closed {
		return
	}
	p.closed = true
	p.a.removeBinder(p.binder)
	if p.a.panel == p {
		p.a.panel = nil
	}
	p.win.Destroy()
}

// placePanel anchors the panel to the screen corner nearest the tray click (global logical
// coordinates from the SNI host), horizontally centred on the click where it fits.
// Unknown position (0,0) falls back to bottom-right of the default monitor.
func placePanel(win *gtk.Window, x, y int) {
	ls := gtk4layershell.SetAnchor
	var mon *gdk.Monitor
	list := gdk.DisplayGetDefault().Monitors()
	for i := uint(0); i < list.NItems(); i++ {
		m, ok := list.Item(i).Cast().(*gdk.Monitor)
		if !ok {
			continue
		}
		g := m.Geometry()
		if x >= g.X() && x < g.X()+g.Width() && y >= g.Y() && y < g.Y()+g.Height() {
			mon = m
		}
	}
	if mon == nil || (x == 0 && y == 0) {
		ls(win, gtk4layershell.LayerShellEdgeBottom, true)
		ls(win, gtk4layershell.LayerShellEdgeRight, true)
		gtk4layershell.SetMargin(win, gtk4layershell.LayerShellEdgeBottom, panelMargin)
		gtk4layershell.SetMargin(win, gtk4layershell.LayerShellEdgeRight, panelMargin)
		return
	}
	gtk4layershell.SetMonitor(win, mon)
	g := mon.Geometry()
	relX, relY := x-g.X(), y-g.Y()
	if relY < g.Height()/2 {
		ls(win, gtk4layershell.LayerShellEdgeTop, true)
		gtk4layershell.SetMargin(win, gtk4layershell.LayerShellEdgeTop, panelMargin)
	} else {
		ls(win, gtk4layershell.LayerShellEdgeBottom, true)
		gtk4layershell.SetMargin(win, gtk4layershell.LayerShellEdgeBottom, panelMargin)
	}
	if relX >= g.Width()/2 {
		ls(win, gtk4layershell.LayerShellEdgeRight, true)
		gtk4layershell.SetMargin(win, gtk4layershell.LayerShellEdgeRight, max(panelMargin, g.Width()-relX-panelWidth/2))
	} else {
		ls(win, gtk4layershell.LayerShellEdgeLeft, true)
		gtk4layershell.SetMargin(win, gtk4layershell.LayerShellEdgeLeft, max(panelMargin, relX-panelWidth/2))
	}
}
