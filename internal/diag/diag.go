// Package diag runs ddcdeck's first-line checks: is every link in the chain (ddcutil,
// i2c permission, the monitor answering, the daemon, the shortcuts portal) working, and
// what should the user do about the ones that are not. Read-only.
package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/godbus/dbus/v5"

	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/location"
)

// State is a check's outcome. "warn" keeps working, "fail" is broken.
const (
	OK   = "ok"
	WARN = "warn"
	FAIL = "fail"
)

// Check is one line of the report. Detail doubles as the fix hint when State is FAIL.
type Check struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// Run walks the whole chain a first command depends on. Calls into DDC go through the
// service (the daemon's socket API in production, an in-process service in the CLI
// fallback), so the read test exercises the same path real commands take.
func Run(svc *deck.Service) []Check {
	var cs []Check
	add := func(name, state, detail string) { cs = append(cs, Check{name, state, detail}) }

	if p, err := exec.LookPath("ddcutil"); err == nil {
		add("ddcutil", OK, p)
	} else {
		add("ddcutil", FAIL, "install it (ddcutil.com) — ddcdeck drives the bus through it")
	}

	if devs, _ := filepath.Glob("/dev/i2c-*"); len(devs) == 0 {
		add("i2c", WARN, "no /dev/i2c-* devices — load the i2c-dev module and connect a DDC monitor")
	} else if f, err := os.OpenFile(devs[0], os.O_RDWR, 0); err == nil {
		f.Close()
		add("i2c", OK, "can talk to "+devs[0])
	} else {
		add("i2c", FAIL, fmt.Sprintf("no permission for %s — run `sudo usermod -aG i2c $USER`, then log out and back in", devs[0]))
	}

	if raw, err := svc.Handle("list", nil); err != nil {
		add("monitors", FAIL, err.Error())
	} else if list, _ := raw.(deck.ListResult); len(list.Monitors) == 0 {
		add("monitors", FAIL, "none detected — check the cable, and enable DDC/CI in the monitor's own settings menu")
	} else {
		for _, m := range list.Monitors {
			detail := fmt.Sprintf("%s %s on %s", m.Mfg, m.Model, m.Connector)
			if m.HasModel {
				add("monitor", OK, detail+", community model file loaded")
			} else {
				add("monitor", WARN, detail+", no model file — `ddcdeck discover` maps its vendor features")
			}
		}
		first := list.Monitors[0]
		raw, _ := json.Marshal(deck.Params{Display: first.ID, Feature: "brightness"})
		if r, err := svc.Handle("get", raw); err != nil {
			add("read test", FAIL, err.Error()+" — DDC/CI is often disabled in the monitor's menu")
		} else if fv, ok := r.(deck.FeatureValue); ok {
			add("read test", OK, fmt.Sprintf("brightness %d/%d on %s", fv.Value, fv.Max, first.Model))
		}
	}

	// reaching the API over the socket means the daemon answered; inside the daemon,
	// dialing its own socket also succeeds — no flag needed either way
	if c, err := deck.Dial(deck.SocketPath()); err == nil {
		c.Close()
		add("daemon", OK, deck.SocketPath())
	} else {
		add("daemon", WARN, "not running — commands still work, but take ~1-3 s each; `make install-service`")
	}

	if portalUp() {
		add("shortcuts", OK, "GlobalShortcuts portal present, keys can be bound")
	} else {
		add("shortcuts", WARN, "no GlobalShortcuts portal — bind keys in your desktop's own shortcut settings")
	}

	if lat, lon := location.Default(); lat != 0 || lon != 0 {
		add("sun location", OK, fmt.Sprintf("%.2f, %.2f — not you? set it in the GUI or [location]", lat, lon))
	}
	return cs
}

// portalUp reports whether a GlobalShortcuts-capable portal is on the session bus.
func portalUp() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	var has bool
	err = conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
		Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.portal.Desktop").Store(&has)
	return err == nil && has
}

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.State == FAIL {
			return true
		}
	}
	return false
}
