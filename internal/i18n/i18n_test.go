package i18n

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestT(t *testing.T) {
	Use("zz_ZZ") // unknown: English passes through
	if got := T("Cancel"); got != "Cancel" {
		t.Fatalf("fallback = %q", got)
	}
	tab = map[string]string{"Cancel": "Abbrechen"}
	if got := T("Cancel"); got != "Abbrechen" {
		t.Fatalf("translated = %q", got)
	}
	if got := T("Other"); got != "Other" {
		t.Fatalf("missing key = %q", got)
	}
}

func TestDetect(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "de_DE.UTF-8")
	if got := Detect(); got != "de_DE" {
		t.Fatalf("Detect = %q", got)
	}
	if c := candidates("de_DE"); len(c) != 2 || c[1] != "de" {
		t.Fatalf("candidates = %v", c)
	}
}

// A contributor's broken locale file must fail CI, not silently show English.
func TestLocalesParse(t *testing.T) {
	ents, err := files.ReadDir("locales")
	if err != nil || len(ents) == 0 {
		t.Fatalf("no locales: %v", err)
	}
	for _, e := range ents {
		b, _ := files.ReadFile("locales/" + e.Name())
		if _, err := toml.Decode(string(b), &map[string]string{}); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}
}
