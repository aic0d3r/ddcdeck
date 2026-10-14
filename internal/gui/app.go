// Package gui is DDC Deck's libadwaita window, tray icon and quick panel. It is a pure client
// of the daemon socket API; all GTK calls happen on the main loop (glib.IdleAdd from goroutines).
package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/shortcuts"
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

// pollInterval: how often an on-screen window re-reads the monitor. The daemon's cache
// cannot notice changes made with the monitor's own OSD buttons, so this read is uncached;
// one batched ddcutil call per monitor is cheap at this rate.
const pollInterval = 15 * time.Second

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

	rules           *RulesInfo
	autoSwitch      *adw.SwitchRow
	autoToggle      *gtk.ToggleButton
	blueSwitch      *adw.SwitchRow // filter row, so the schedule can speak through it
	blueSpec        string         // "off", "sunset", "HH:MM-HH:MM"
	blueOn          bool
	placeEntry      *gtk.Entry
	placeRow        *adw.ActionRow
	zoneStore       *gtk.TreeStore
	rulesGroup      *adw.PreferencesGroup
	ruleRows        []*adw.ActionRow
	shortcutsGroup  *adw.PreferencesGroup
	shortcutRows    []*adw.ActionRow
	shortcutsErr    string
	shortcutsSig    string
	scm             *shortcuts.Manager
	updateTrayPause func(on bool)
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
		src := glib.NewTimeoutSource(uint(pollInterval / time.Millisecond))
		src.Attach(nil)
		src.SetCallback(func() bool {
			if a.cl != nil && a.cur != nil && a.win.win.IsVisible() && !a.win.win.IsActive() {
				a.refreshFresh() // values changed on the monitor itself show up by themselves;
				// skipped while focused, where an uncached read would fight slider drags
			}
			return true
		})
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
	a.refreshAutomation()
}

func (a *App) quit() {
	if a.panel != nil {
		a.panel.close()
	}
	if a.scm != nil {
		a.scm.Close()
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
			var rules RulesInfo
			_ = cl.Call("rules.list", nil, &rules) // old daemons: ignore, page stays empty
			idle(func() {
				a.cl = cl
				a.setMonitors(list.Monitors)
				a.bindShortcuts(rules.Shortcuts)
				a.refreshAutomation()
			})
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}          // outlives the GUI
	cmd.Env = append(os.Environ(), "DDCDECK_LOG="+deck.LogPath()) // our stderr goes nowhere; `ddcdeck daemon logs` reads the file
	if err := cmd.Start(); err != nil {
		return err
	}
	slog.Info("started daemon", "bin", bin, "pid", cmd.Process.Pid)
	go cmd.Wait()
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// call runs an API call off the main loop and delivers the result back on it.
// p may be deck.Params or any JSON-marshalable value.
func (a *App) call(method string, p any, out any, done func(error)) {
	cl := a.cl
	if cl == nil {
		if done != nil {
			done(errors.New("not connected to the daemon"))
		}
		return
	}
	if dp, ok := p.(deck.Params); ok && dp.Display == "" && a.cur != nil {
		dp.Display = a.cur.ID
		p = dp
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
		a.cur = deck.Match(mons, deck.DefaultDisplay()) // the pinned monitor wins over the first one
		if a.cur == nil {
			a.cur = mons[0]
		}
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
	a.refreshAutomation()
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
func (a *App) refresh() { a.status(false) }

// refreshFresh re-reads the monitor itself, so changes made in its OSD show up.
func (a *App) refreshFresh() { a.status(true) }

func (a *App) status(fresh bool) {
	if a.cur == nil {
		return
	}
	id := a.cur.ID
	var res deck.StatusResult
	a.call("status", deck.Params{Fresh: fresh}, &res, func(err error) {
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
	case "auto":
		if a.rules != nil {
			a.rules.Auto = e.State == "running"
		}
		a.syncing = true
		if a.autoSwitch != nil {
			a.autoSwitch.SetActive(e.State == "running")
		}
		if a.autoToggle != nil {
			syncAutoToggle(a.autoToggle, e.State == "running")
		}
		if a.updateTrayPause != nil {
			a.updateTrayPause(e.State == "running")
		}
		a.syncing = false
	case "profile":
		if e.State == "saved" {
			a.win.rebuildProfiles()
		}
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

// autoOn reports the last known automation state.
func (a *App) autoOn() bool { return a.rules != nil && a.rules.Auto }

func (a *App) addRuleRow(r *adw.ActionRow) { a.ruleRows = append(a.ruleRows, r); a.rulesGroup.Add(r) }
func (a *App) addShortcutRow(r *adw.ActionRow) {
	a.shortcutRows = append(a.shortcutRows, r)
	a.shortcutsGroup.Add(r)
}

// bindShortcuts registers global shortcuts via the portal, from the GUI process: the
// portal derives the app id from the systemd scope, which only desktop-launched apps have.
func (a *App) bindShortcuts(scs []ShortcutInfo) {
	sig := fmt.Sprint(scs)
	if sig == a.shortcutsSig {
		return
	}
	a.shortcutsSig = sig
	mgr := a.scm
	if mgr == nil {
		mgr = shortcuts.New()
		a.scm = mgr
		go func() {
			for id := range mgr.Activations() { // ends with the process; a closed Manager just stops delivering
				id := id
				idle(func() { a.runAction(id) })
			}
		}()
	}
	if len(scs) == 0 {
		a.scm = nil // Close is final: the next non-empty list builds a fresh Manager
		go func() {
			mgr.Close()
			idle(func() { a.shortcutsErr = "" })
		}()
		return
	}
	var list []shortcuts.Shortcut
	for _, sc := range scs {
		list = append(list, shortcuts.Shortcut{ID: sc.Action, Description: sc.Action + " (" + sc.Keys + ")",
			Preferred: shortcuts.Trigger(sc.Keys)})
	}
	go func() { // the portal can take seconds to answer: keep it off the GTK main loop
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := mgr.Bind(ctx, list)
		idle(func() {
			if err != nil {
				a.shortcutsErr = err.Error()
				if strings.Contains(err.Error(), "app id") { // launched from a terminal: no .desktop id
					a.shortcutsErr = "Start DDC Deck from the app menu to enable them"
				}
				slog.Warn("global shortcuts not bound", "err", err)
			} else {
				a.shortcutsErr = ""
			}
			a.refreshAutomation()
		})
	}()
}

func (a *App) runAction(action string) {
	a.call("action.run", map[string]any{"action": action}, nil, func(err error) {
		if err != nil {
			a.toast(err.Error())
		}
	})
}

// syncAutoToggle keeps the header button honest: paused reads at a glance (pause glyph,
// released), running is the quiet state; the tooltip spells both out.
func syncAutoToggle(t *gtk.ToggleButton, running bool) {
	t.SetActive(running)
	t.SetIconName(map[bool]string{true: "alarm-symbolic", false: "media-playback-pause-symbolic"}[running])
	t.SetTooltipText(map[bool]string{true: "Automation running — click to pause", false: "Automation paused — click to resume"}[running])
}

// updateBlueSub lets the filter row speak with the schedule's voice when one is set:
// "off" next to a sunset schedule means "off until sunset", and the row should say so.
func (a *App) updateBlueSub() {
	if a.blueSwitch == nil {
		return
	}
	sub := "Warmer colors for the evening"
	if a.blueSpec != "" && a.blueSpec != "off" {
		on, off := "sunset", "sunrise"
		if a.blueSpec != "sunset" {
			on, off, _ = strings.Cut(a.blueSpec, "-")
		}
		if a.blueOn {
			sub = "On — schedule turns it off at " + off
		} else {
			sub = "Off — schedule turns it on at " + on
		}
	}
	a.blueSwitch.SetSubtitle(sub)
}
