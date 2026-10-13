// ddcdeck-gui — DDC Deck window and tray. `--hidden` starts in the tray (used by autostart).
package main

import (
	"log/slog"
	"os"
	"slices"

	"github.com/aic0d3r/ddcdeck/internal/gui"
)

func main() {
	// gotk4 forwards all GLib messages (incl. Vulkan loader chatter) to slog at Info
	level := slog.LevelWarn
	if os.Getenv("DDCDECK_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	os.Exit(gui.Run(slices.Contains(os.Args[1:], "--hidden")))
}
