package shortcuts

import "testing"

func TestTrigger(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Meta+Shift+B", "LOGO+SHIFT+b"},
		{"Meta+Shift+Up", "LOGO+SHIFT+Up"},
		{"ctrl+alt+Delete", "CTRL+ALT+Delete"},
		{"Win+F5", "LOGO+F5"},
		{"PrintScreen", "Print"},
		{"Ctrl++", "CTRL+plus"}, // the key itself is "+"
		{"Meta+Shift++", "LOGO+SHIFT+plus"},
		{"b", "b"},
		{"", ""},
		{"   ", ""},
		{"Hyper+B", "LOGO+b"},
		{"Banana+B", ""}, // unknown modifier: better no suggestion than a wrong one
	} {
		if got := Trigger(tc.in); got != tc.want {
			t.Errorf("Trigger(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
