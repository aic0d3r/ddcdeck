// Package notify shows desktop notifications over org.freedesktop.Notifications.
// Everything is best effort: automation has to keep working when no notification
// server is running.
package notify

import (
	"log/slog"

	"github.com/godbus/dbus/v5"
)

const appID = "io.github.aic0d3r.DDCDeck" // desktop file id: icon and "do not disturb" grouping

// Send shows one notification. A fresh bus connection per call is fine here: rules fire
// a handful of times a day, not per frame.
func Send(summary, body string) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		slog.Debug("notify: no session bus", "err", err)
		return
	}
	defer conn.Close()
	err = conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").
		Call("org.freedesktop.Notifications.Notify", 0,
			"DDC Deck", uint32(0), appID, summary, body,
			[]string{}, map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant(appID)}, int32(0)).Err
	if err != nil {
		slog.Debug("notify: send failed", "err", err)
	}
}
