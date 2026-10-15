package appwatch

import "testing"

func TestHyprClass(t *testing.T) {
	for line, want := range map[string]string{
		"activewindow>>kitty,~ : zsh, ok": "kitty",
		"activewindow>>,":                 "",
		"openwindow>>1,2,3,4":             "",
	} {
		if got, ok := hyprClass(line); got != want || ok != (want != "") {
			t.Errorf("hyprClass(%q) = %q,%v; want %q", line, got, ok, want)
		}
	}
}

func TestSwayClass(t *testing.T) {
	for line, want := range map[string]string{
		`{"change":"focus","container":{"app_id":"kitty"}}`:                                  "kitty",
		`{"change":"focus","container":{"app_id":"","window_properties":{"class":"Steam"}}}`: "Steam",
		`{"change":"title","container":{"app_id":"kitty"}}`:                                  "",
		`not json`: "",
	} {
		if got, ok := swayClass(line); got != want || ok != (want != "") {
			t.Errorf("swayClass(%q) = %q,%v; want %q", line, got, ok, want)
		}
	}
}
