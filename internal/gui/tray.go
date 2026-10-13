package gui

import (
	_ "embed"
	"log/slog"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"
)

//go:embed tray.png
var trayIcon []byte

// startTray registers the StatusNotifierItem: left-click toggles the quick panel,
// right-click shows the menu, scrolling changes brightness. Callbacks arrive on D-Bus goroutines, so hop to the main loop.
func startTray(a *App) {
	systray.SetTitle("DDC Deck") // also the SNI Id, so the tray remembers our visibility setting
	systray.SetTooltip("DDC Deck")
	systray.SetIcon(trayIcon)
	systray.SetOnTapped(func() {}) // must be set before start: makes ItemIsMenu false; real handler below
	start, _ := systray.RunWithExternalLoop(func() {
		open := systray.AddMenuItem("Open DDC Deck", "")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					idle(a.showWindow)
				case <-quit.ClickedCh:
					idle(a.quit)
				}
			}
		}()
	}, nil)
	start()
	// fyne/systray drops Activate's click position and ignores Scroll. It exports on the shared
	// session connection, so re-exporting the same interface on the same path replaces its
	// handlers with ours (properties and the dbusmenu stay fyne's).
	conn, err := dbus.SessionBus()
	if err == nil {
		err = conn.ExportSubtreeMethodTable(map[string]any{
			"Activate": func(x, y int32) *dbus.Error {
				idle(func() { a.togglePanelAt(int(x), int(y)) })
				return nil
			},
			"SecondaryActivate": func(x, y int32) *dbus.Error { idle(a.showWindow); return nil },
			"ContextMenu":       func(x, y int32) *dbus.Error { return &dbus.ErrMsgUnknownMethod }, // host shows the dbusmenu
			"Scroll": func(delta int32, orientation string) *dbus.Error {
				if orientation == "vertical" && delta != 0 {
					step := map[bool]string{true: "+5", false: "-5"}[delta > 0]
					idle(func() { a.set("brightness", step) })
				}
				return nil
			},
		}, "/StatusNotifierItem", "org.kde.StatusNotifierItem")
	}
	if err != nil {
		slog.Warn("tray: click position and scroll unavailable", "err", err)
	}
}
