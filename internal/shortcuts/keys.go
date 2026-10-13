package shortcuts

import (
	"regexp"
	"slices"
	"strings"
)

// Trigger converts a rules.toml key string ("Meta+Shift+B") to the form the portal's
// preferred_trigger option wants: the freedesktop shortcuts-spec, i.e. xkbcommon modifier
// names and a keysym name, joined by "+": "LOGO+SHIFT+b". Returns "" when it can't tell
// what the keys mean — a wrong guess is worse than no suggestion.
func Trigger(keys string) string {
	if strings.TrimSpace(keys) == "" {
		return ""
	}
	toks := strings.Split(strings.TrimSpace(keys), "+")
	for i := range toks {
		toks[i] = strings.TrimSpace(toks[i])
	}
	if last := len(toks) - 1; toks[last] == "" {
		toks[last] = "plus" // "Ctrl++" is Control plus the plus key
	}
	toks = slices.DeleteFunc(toks, func(s string) bool { return s == "" })
	if len(toks) == 0 {
		return ""
	}
	out := make([]string, 0, len(toks))
	for _, t := range toks[:len(toks)-1] {
		m, ok := modifiers[strings.ToLower(t)]
		if !ok {
			return ""
		}
		out = append(out, m)
	}
	return strings.Join(append(out, keysym(toks[len(toks)-1])), "+")
}

// modifiers as people write them in rules.toml → xkbcommon modifier names.
var modifiers = map[string]string{
	"ctrl": "CTRL", "control": "CTRL", "alt": "ALT", "shift": "SHIFT",
	"meta": "LOGO", "super": "LOGO", "win": "LOGO", "hyper": "LOGO", "num": "NUM",
}

// keysym maps the names people type to keysym names (xkbcommon-keysyms.h minus XKB_KEY_).
var keysymAlias = map[string]string{
	"enter": "Return", "space": "space", "pgup": "Prior", "pageup": "Prior",
	"pgdn": "Next", "pagedown": "Next", "print": "Print", "printscreen": "Print",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "insert": "Insert", "delete": "Delete",
	"plus": "plus", "minus": "minus",
}

var fkey = regexp.MustCompile(`^F([1-9]|1[0-2])$`)

func keysym(k string) string {
	if len(k) == 1 {
		return strings.ToLower(k)
	}
	if v, ok := keysymAlias[strings.ToLower(k)]; ok {
		return v
	}
	if fkey.MatchString(strings.ToUpper(k)) {
		return strings.ToUpper(k)
	}
	return k
}
