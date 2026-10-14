package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const autostartFile = "io.github.aic0d3r.DDCDeck.desktop"

// appVersion is the build's VCS revision; the GUI binary doesn't carry cmd.Version.
func appVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

func autostartPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "autostart", autostartFile)
}

// setAutostart writes or removes the login entry `make install` also creates.
func setAutostart(on bool) error {
	path := autostartPath()
	if path == "" {
		return fmt.Errorf("no config directory")
	}
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=DDC Deck\n"+
		"Comment=Monitor brightness, blue light filter, color and input over DDC/CI\n"+
		"Exec=%q --hidden\nIcon=io.github.aic0d3r.DDCDeck\n", exe)
	return os.WriteFile(path, []byte(entry), 0o644)
}

// buildAppSettings is the app-wide page: nothing here depends on which monitor is selected.
func buildAppSettings(w *window) *adw.PreferencesPage {
	a := w.a

	startup := adw.NewPreferencesGroup()
	startup.SetTitle("Startup")
	sw := adw.NewSwitchRow()
	sw.SetTitle("Start at login")
	sw.SetSubtitle("Opens DDC Deck in the tray so automation keeps running.")
	_, err := os.Stat(autostartPath())
	sw.SetActive(err == nil)
	settingBack := false
	sw.NotifyProperty("active", func() {
		if settingBack {
			return
		}
		if err := setAutostart(sw.Active()); err != nil {
			a.toast("Couldn't change start at login: " + err.Error())
			settingBack = true
			sw.SetActive(!sw.Active())
			settingBack = false
		}
	})
	startup.Add(sw)

	about := adw.NewPreferencesGroup()
	about.SetTitle("About")
	for _, kv := range [][2]string{{"Version", appVersion()}, {"License", "GPL-3.0-only"}} {
		r := adw.NewActionRow()
		r.SetTitle(kv[0])
		r.SetSubtitle(kv[1])
		r.SetSubtitleSelectable(true)
		r.AddCSSClass("property")
		about.Add(r)
	}
	src := adw.NewActionRow()
	src.SetTitle("Source code and bug reports")
	src.SetSubtitle(model.RepoURL)
	open := gtk.NewButtonWithLabel("Open")
	open.SetVAlign(gtk.AlignCenter)
	open.ConnectClicked(func() { gtk.NewURILauncher(model.RepoURL).Launch(context.Background(), &w.win.Window, nil) })
	src.AddSuffix(open)
	about.Add(src)

	return prefsPage(startup, buildDiagnostics(w), about)
}

// unverifiedTitles names the vendor features of the loaded model file that nobody has
// confirmed on hardware (imported from a public database, or still guesses).
func unverifiedTitles(m *deck.Monitor) []string {
	var out []string
	for name, f := range m.Features {
		if _, std := model.Defaults()[name]; !std && !f.Verified {
			out = append(out, title(name))
		}
	}
	sort.Strings(out)
	return out
}

// unverifiedRow tells the user those features may be wrong for this monitor and lets them
// report back in one click (a prefilled GitHub issue, reviewed before sending).
func unverifiedRow(w *window, names []string) *adw.ActionRow {
	m := w.a.cur
	id := fmt.Sprintf("%s-%s-%d", m.Mfg, m.Model, m.Product)
	r := adw.NewActionRow()
	r.SetTitle("Not verified on hardware")
	r.SetSubtitle(strings.Join(names, ", ") + " came from a public database and may be wrong for this monitor. Does it work?")
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetVAlign(gtk.AlignCenter)
	for _, c := range []struct {
		label string
		works bool
	}{{"Works", true}, {"Doesn't work", false}} {
		b := gtk.NewButtonWithLabel(c.label)
		b.SetTooltipText("Opens a prefilled GitHub issue; you review it before sending.")
		b.ConnectClicked(func() {
			gtk.NewURILauncher(model.FeedbackURL(id, c.works, names, appVersion())).Launch(context.Background(), &w.win.Window, nil)
		})
		box.Append(b)
	}
	r.AddSuffix(box)
	return r
}
