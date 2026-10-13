// Package appwatch reports the active window class to the automation engine on KDE
// Plasma: a small KWin script calls a D-Bus method the daemon exports. The class is
// KWin's resourceClass, i.e. the application id ("kitty", "io.github.aic0d3r.DDCDeck").
// AT-SPI focus events were tried first (cross-desktop) but event delivery is disabled
// on this system, so v1 ships KWin-only, as the design planned.
package appwatch

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

//go:embed watcher.js
var watcherJS string

const (
	busName   = "io.github.aic0d3r.ddcdeckd" // the GUI owns io.github.aic0d3r.DDCDeck
	objPath   = dbus.ObjectPath("/io/github/aic0d3r/ddcdeckd/app")
	iface     = "io.github.aic0d3r.ddcdeckd"
	scriptID  = "ddcdeck-appwatch"
	kwinBus   = "org.kde.KWin"
	scripting = "org.kde.kwin.Scripting"
)

// Watcher owns the D-Bus name and keeps the KWin script loaded.
type Watcher struct {
	conn    *dbus.Conn
	onClass func(string)

	mu      sync.Mutex
	loaded  bool
	stopped bool
}

// Start exports SetClass and loads the KWin script, reloading it if KWin restarts.
// Without KWin it logs and returns an inactive watcher; app rules just don't fire.
func Start(ctx context.Context, onClass func(string)) (*Watcher, error) {
	w := &Watcher{onClass: onClass}
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	w.conn = conn
	if err := conn.ExportMethodTable(map[string]any{"SetClass": w.setClass}, objPath, iface); err != nil {
		return nil, err
	}
	if reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("D-Bus name %s unavailable: %v", busName, err)
	}
	if err := w.loadScript(); err != nil {
		slog.Info("app watcher inactive (no KWin?)", "err", err)
	} else {
		w.mu.Lock()
		w.loaded = true
		w.mu.Unlock()
		slog.Info("app watcher active (KWin script)")
	}
	go w.watchKWinRestarts(ctx)
	go func() {
		<-ctx.Done()
		w.Stop()
	}()
	return w, nil
}

func (w *Watcher) setClass(class string) *dbus.Error {
	if c := strings.TrimSpace(class); c != "" {
		w.onClass(c)
	}
	return nil
}

func (w *Watcher) loadScript() error {
	obj := w.conn.Object(kwinBus, "/Scripting")
	if r := obj.Call(scripting+".isScriptLoaded", 0, scriptID); r.Err == nil {
		if loaded, _ := r.Body[0].(bool); loaded {
			return nil
		}
	}
	path, err := installScript()
	if err != nil {
		return err
	}
	if r := obj.Call(scripting+".loadScript", 0, path, scriptID); r.Err != nil {
		return r.Err
	}
	if r := obj.Call(scripting+".start", 0); r.Err != nil {
		return r.Err
	}
	return nil
}

// watchKWinRestarts reloads the script when KWin comes back (it forgets scripts).
func (w *Watcher) watchKWinRestarts(ctx context.Context) {
	ch := make(chan *dbus.Signal, 4)
	w.conn.Signal(ch)
	if err := w.conn.AddMatchSignal(dbus.WithMatchObjectPath("/org/freedesktop/DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged")); err != nil {
		slog.Warn("app watcher: cannot watch KWin restarts", "err", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-ch:
			if len(sig.Body) < 3 {
				continue
			}
			name, oldOwner, newOwner := sig.Body[0].(string), sig.Body[1].(string), sig.Body[2].(string)
			if name != kwinBus || oldOwner == "" || newOwner == "" {
				continue
			}
			w.mu.Lock()
			loaded := w.loaded && !w.stopped
			w.mu.Unlock()
			if !loaded {
				continue
			}
			slog.Info("KWin restarted, reloading app watcher")
			time.Sleep(2 * time.Second)
			if err := w.loadScript(); err != nil {
				slog.Warn("app watcher reload failed", "err", err)
			}
		}
	}
}

// installScript writes the embedded JS where KWin's loadScript can read it.
func installScript() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "ddcdeck", "kwin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, scriptID+".js")
	return path, os.WriteFile(path, []byte(watcherJS), 0o600)
}

// Active reports whether the KWin script is loaded.
func (w *Watcher) Active() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.loaded
}

// Stop unloads the script and releases the D-Bus name (idempotent).
func (w *Watcher) Stop() {
	w.mu.Lock()
	w.stopped = true
	loaded := w.loaded
	w.loaded = false
	w.mu.Unlock()
	if loaded {
		w.conn.Object(kwinBus, "/Scripting").Call(scripting+".unloadScript", 0, scriptID)
	}
	_, _ = w.conn.ReleaseName(busName)
}
