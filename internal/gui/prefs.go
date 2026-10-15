package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// guiPrefs are the window's own settings (not monitor state, so not the daemon's business).
type guiPrefs struct {
	QuitOnClose  bool                         `json:"quit_on_close,omitempty"` // default: close hides to the tray
	LinkMonitors bool                         `json:"link_monitors,omitempty"` // sliders drive every monitor
	InputLabels  map[string]map[string]string `json:"input_labels,omitempty"`  // monitor ID → input key → your name
}

func prefsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ddcdeck", "gui.json")
}

func loadPrefs() guiPrefs {
	var p guiPrefs
	if b, err := os.ReadFile(prefsPath()); err == nil {
		_ = json.Unmarshal(b, &p) // a corrupt file just means defaults
	}
	return p
}

func (a *App) savePrefs() {
	path := prefsPath()
	if path == "" {
		return
	}
	b, _ := json.MarshalIndent(a.prefs, "", "  ")
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

// inputLabel is the user's name for an input of the current monitor, else the stock one.
func (a *App) inputLabel(key string) string {
	if l := a.prefs.InputLabels[a.cur.ID][key]; l != "" {
		return l
	}
	return valueLabel(key)
}
