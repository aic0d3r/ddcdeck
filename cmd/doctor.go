package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/godbus/dbus/v5"
	"github.com/spf13/cobra"

	"github.com/aic0d3r/ddcdeck/internal/automation"
	"github.com/aic0d3r/ddcdeck/internal/deck"
)

// doctor walks the whole chain a first command depends on, and says how to fix each break:
// ddcutil, i2c permission, the monitor answering, model files, the daemon, the shortcuts
// portal, and the sun-rule location. Read-only; exits 1 when something is broken.
func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check everything ddcdeck needs, and how to fix what is missing",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return doctor() },
	})
}

func doctor() error {
	failed := false
	ok := func(name, detail string) { fmt.Printf("[ ok ] %-16s %s\n", name, detail) }
	warn := func(name, detail string) { fmt.Printf("[warn] %-16s %s\n", name, detail) }
	bad := func(name, fix string) { failed = true; fmt.Printf("[FAIL] %-16s %s\n", name, fix) }

	if p, err := exec.LookPath("ddcutil"); err == nil {
		ok("ddcutil", p)
	} else {
		bad("ddcutil", "install it (ddcutil.com) — ddcdeck drives the bus through it")
	}

	if devs, _ := filepath.Glob("/dev/i2c-*"); len(devs) == 0 {
		warn("i2c", "no /dev/i2c-* devices — load the i2c-dev module and connect a DDC monitor")
	} else if canWrite(devs[0]) {
		ok("i2c", "can talk to "+devs[0])
	} else {
		bad("i2c", fmt.Sprintf("no permission for %s — run `sudo usermod -aG i2c $USER`, then log out and back in", devs[0]))
	}

	// one connection for the rest: without the daemon each call would re-scan (~2 s each)
	locShown := false
	if c, err := connect(); err != nil {
		bad("monitors", err.Error())
	} else {
		var list deck.ListResult
		if err := c.Call("list", nil, &list); err != nil {
			bad("monitors", err.Error())
		} else if len(list.Monitors) == 0 {
			bad("monitors", "none detected — check the cable, and enable DDC/CI in the monitor's own settings menu")
		} else {
			for _, m := range list.Monitors {
				detail := fmt.Sprintf("%s %s on %s", m.Mfg, m.Model, m.Connector)
				if m.HasModel {
					ok("monitor", detail+", community model file loaded")
				} else {
					warn("monitor", detail+", no model file — `ddcdeck discover` maps its vendor features")
				}
			}
			var fv deck.FeatureValue
			if err := c.Call("get", deck.Params{Display: list.Monitors[0].ID, Feature: "brightness"}, &fv); err != nil {
				bad("read test", err.Error()+" — DDC/CI is often disabled in the monitor's menu")
			} else {
				ok("read test", fmt.Sprintf("brightness %d/%d on %s", fv.Value, fv.Max, list.Monitors[0].Model))
			}
			var rr rulesResult
			if err := c.Call("rules.list", nil, &rr); err == nil {
				ok("sun location", fmt.Sprintf("%.2f, %.2f — not you? set it in the GUI or [location]", rr.Location.Lat, rr.Location.Lon))
				locShown = true
			}
		}
	}

	if c, err := deck.Dial(deck.SocketPath()); err == nil {
		c.Close()
		ok("daemon", deck.SocketPath())
	} else {
		warn("daemon", "not running — commands still work, but take ~1-3 s each; `make install-service`")
	}

	if portalUp() {
		ok("shortcuts", "GlobalShortcuts portal present, keys can be bound")
	} else {
		warn("shortcuts", "no GlobalShortcuts portal — bind keys in your desktop's own shortcut settings")
	}

	// the fallback location is what sun rules use when nothing is configured
	if !locShown {
		lat, lon := automation.LocationDefault()
		fmt.Printf("[info] %-16s timezone default %.2f, %.2f\n", "sun location", lat, lon)
	}
	if failed {
		return errors.New("some checks failed — follow the hints above")
	}
	fmt.Println("all good")
	return nil
}

func canWrite(dev string) bool {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err == nil {
		f.Close()
		return true
	}
	return false
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
