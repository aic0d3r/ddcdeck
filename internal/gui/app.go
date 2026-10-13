// Package gui is DDC Deck's libadwaita window, tray icon and quick panel. It is a pure client
// of the daemon socket API; all GTK calls happen on the main loop (glib.IdleAdd from goroutines).
package gui

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
)

// panelCSS rounds the quick panel: transparent surface, rounded card inside.
const panelCSS = `
window.ddcdeck-panel { background: transparent; }
.ddcdeck-panel-box { background: var(--window-bg-color); border-radius: 14px;
  border: 1px solid var(--border-color); padding: 12px; }
/* fixed slider widths keep the tracks aligned across rows */
.ddcdeck-main scale { min-width: 260px; }
.ddcdeck-panel scale { min-width: 170px; }
`

// AppID is the GApplication / desktop file id.
const AppID = "io.github.aic0d3r.DDCDeck"

// echoWindow: after a local change, ignore that feature's events for this long so our own
// optimistic echoes (which lag a dragged slider) don't make it jump back.
const echoWindow = 800 * time.Millisecond

// binder holds one view's widget updaters, keyed by feature.
type binder map[string][]func(deck.FeatureValue)

func (b binder) on(feature string, fn func(deck.FeatureValue)) { b[feature] = append(b[feature], fn) }

// App is the GUI state. Everything except cl's goroutines runs on the GTK main loop.
type App struct {
	app     *adw.Application
	cl      *deck.Client // nil while disconnected
	mons    []*deck.Monitor
	cur     *deck.Monitor
	vals    map[string]deck.FeatureValue
	binders map[*binder]struct{}
	localAt map[string]time.Time
	seq     map[string]int // per-feature count of local sets; only the newest reply is applied
	syncing bool           // true while widgets are updated programmatically: their handlers must not send

	win     *window
	panel   *panel
	hidden  bool
	spawned time.Time
}

// Run starts the application; hidden starts it in the tray without showing the window.
func Run(hidden bool) int {
	a := &App{
		app:     adw.NewApplication(AppID, gio.ApplicationFlagsNone),
		vals:    map[string]deck.FeatureValue{},
		binders: map[*binder]struct{}{},
		localAt: map[string]time.Time{},
		seq:     map[string]int{},
		hidden:  hidden,
	}
	a.app.ConnectStartup(func() {
		a.app.Hold() // keep running in the tray with no visible window
		gtk.WindowSetDefaultIconName(AppID)
		css := gtk.NewCSSProvider()
		css.LoadFromString(panelCSS)
		gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), css, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
		a.win = newWindow(a)
		startTray(a)
		go a.connectLoop()
	})
	a.app.ConnectActivate(func() {
		if a.hidden { // only the autostart launch is hidden; later activations show the window
			a.hidden = false
			return
		}
		a.showWindow()
	})
	return a.app.Run([]string{os.Args[0]})
}

func idle(fn func()) { glib.IdleAdd(fn) }

func (a *App) showWindow() {
	a.win.win.Present()
	a.refresh()
}

func (a *App) quit() {
	if a.panel != nil {
		a.panel.close()
	}
	a.app.Quit()
}

// connectLoop keeps one daemon connection alive, starting the daemon if needed.
func (a *App) connectLoop() {
	for {
		cl, err := a.dial()
		if err != nil {
			slog.Warn("daemon unavailable", "err", err)
			idle(func() { a.setDisconnected("Can't reach the DDC Deck daemon", err.Error()) })
			time.Sleep(5 * time.Second)
			continue
		}
		err = cl.Subscribe(func(e deck.Event) { idle(func() { a.onEvent(e) }) })
		var list deck.ListResult
		if err == nil {
			err = cl.Call("list", nil, &list)
		}
		if err == nil {
			idle(func() { a.cl = cl; a.setMonitors(list.Monitors) })
			<-cl.Done()
		}
		cl.Close()
		idle(func() { a.cl = nil; a.setDisconnected("Reconnecting to the daemon…", "") })
		time.Sleep(time.Second)
	}
}

// dial connects, starting ddcdeckd via systemd or as a detached process when it isn't running.
func (a *App) dial() (*deck.Client, error) {
	path := deck.SocketPath()
	if cl, err := deck.Dial(path); err == nil {
		return cl, nil
	}
	if exec.Command("systemctl", "--user", "start", "ddcdeckd").Run() != nil && time.Since(a.spawned) > 30*time.Second {
		a.spawned = time.Now()
		if err := spawnDaemon(); err != nil {
			return nil, err
		}
	}
	for i := 0; i < 40; i++ { // initial scan takes ~2 s per monitor
		time.Sleep(250 * time.Millisecond)
		if cl, err := deck.Dial(path); err == nil {
			return cl, nil
		}
	}
	return nil, errors.New("daemon did not start; check `ddcdeck daemon run`")
}

func spawnDaemon() error {
	bin, err := exec.LookPath("ddcdeckd")
	if self, e := os.Executable(); e == nil {
		if p := filepath.Join(filepath.Dir(self), "ddcdeckd"); fileExists(p) {
			bin, err = p, nil
		}
	}
	if err != nil {
		return errors.New("ddcdeckd not found; run `make install`")
	}
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives the GUI
	if err := cmd.Start(); err != nil {
		return err
	}
	slog.Info("started daemon", "bin", bin, "pid", cmd.Process.Pid)
	go cmd.Wait()
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// call runs an API call off the main loop and delivers the result back on it.
func (a *App) call(method string, p deck.Params, out any, done func(error)) {
	cl := a.cl
	if cl == nil {
		if done != nil {
			done(errors.New("not connected to the daemon"))
		}
		return
	}
	if p.Display == "" && a.cur != nil {
		p.Display = a.cur.ID
	}
	wait := cl.Go(method, p, out) // send now: sets must reach the daemon in UI order
	go func() {
		err := wait()
		if done != nil {
			idle(func() { done(err) })
		}
	}()
}

func (a *App) setDisconnected(title, detail string) {
	a.mons, a.cur = nil, nil
	a.win.showStatus(title, detail)
	if a.panel != nil {
		a.panel.close()
	}
}

func (a *App) setMonitors(mons []*deck.Monitor) {
	a.mons = mons
	keep := a.cur
	a.cur = nil
	for _, m := range mons {
		if keep != nil && m.ID == keep.ID {
			a.cur = m
		}
	}
	if a.cur == nil && len(mons) > 0 {
		a.cur = mons[0]
	}
	a.vals = map[string]deck.FeatureValue{}
	if a.cur == nil {
		a.win.showStatus("No DDC monitor found", "Connect a monitor with DDC/CI enabled in its menu.")
	} else {
		a.win.rebuild()
	}
	if a.panel != nil {
		a.panel.close()
	}
	a.refresh()
}

func (a *App) selectMonitor(i int) {
	if i < 0 || i >= len(a.mons) || a.mons[i] == a.cur {
		return
	}
	a.cur = a.mons[i]
	a.vals = map[string]deck.FeatureValue{}
	a.win.rebuild()
	a.refresh()
}

// refresh reads all values of the current monitor (served from the daemon cache when fresh).
func (a *App) refresh() {
	if a.cur == nil {
		return
	}
	id := a.cur.ID
	var res deck.StatusResult
	a.call("status", deck.Params{}, &res, func(err error) {
		if err != nil {
			a.toast("Couldn't read the monitor: " + err.Error())
			return
		}
		if a.cur == nil || a.cur.ID != id {
			return
		}
		for _, fv := range res.Values {
			a.apply(fv)
		}
	})
}

// apply stores a value and pushes it to every bound widget.
func (a *App) apply(fv deck.FeatureValue) {
	a.vals[fv.Feature] = fv
	a.syncing = true
	defer func() { a.syncing = false }()
	for b := range a.binders {
		for _, fn := range (*b)[fv.Feature] {
			fn(fv)
		}
	}
}

func (a *App) addBinder(b *binder)    { a.binders[b] = struct{}{} }
func (a *App) removeBinder(b *binder) { delete(a.binders, b) }

// set sends a user change. Called from widget handlers; ignored while syncing.
func (a *App) set(feature, value string) {
	if a.syncing || a.cur == nil {
		return
	}
	a.localAt[feature] = time.Now()
	a.seq[feature]++
	n := a.seq[feature]
	raw, _ := json.Marshal(value)
	var fv deck.FeatureValue
	a.call("set", deck.Params{Feature: feature, Value: raw}, &fv, func(err error) {
		if err != nil {
			a.toast(err.Error())
			a.refresh()
			return
		}
		if a.seq[feature] == n { // newest change: reflect it in the other view (window/panel)
			a.apply(fv)
		}
	})
}

func (a *App) onEvent(e deck.Event) {
	switch e.Event {
	case "hotplug":
		var list deck.ListResult
		a.call("list", deck.Params{}, &list, func(err error) {
			if err == nil {
				a.setMonitors(list.Monitors)
				a.toast(map[string]string{"connected": "Monitor connected", "disconnected": "Monitor disconnected"}[e.State])
			}
		})
	case "value":
		if a.cur == nil || e.Display != a.cur.ID || e.Value == nil || time.Since(a.localAt[e.Feature]) < echoWindow {
			return
		}
		f, ok := a.cur.Features[e.Feature]
		if !ok {
			return
		}
		a.apply(deck.Present(e.Feature, f, ddc.Value{Cur: *e.Value, Max: a.vals[e.Feature].Max}))
	}
}

func (a *App) toast(msg string) {
	if msg != "" {
		a.win.toasts.AddToast(adw.NewToast(msg))
	}
}
