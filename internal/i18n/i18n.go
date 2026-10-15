// Package i18n translates the GUI's fixed strings. The English text is the key: T returns
// it unchanged when there is no translation, so a missing or partial locale never breaks
// anything. Locales are TOML files in locales/ (`"English text" = "Translation"`); add
// one by copying locales/template.toml to locales/<lang>.toml - no code change.
package i18n

import (
	"embed"
	"os"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

//go:embed locales/*.toml
var files embed.FS

var (
	mu  sync.RWMutex
	tab map[string]string
)

func init() { Use(Detect()) }

// Detect returns the user's locale from the usual environment variables, e.g. "de_DE".
func Detect() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			l, _, _ := strings.Cut(v, ".")
			l, _, _ = strings.Cut(l, "@")
			return l
		}
	}
	return ""
}

// Use loads the translations for a locale ("de_DE" tries de_DE, then de). An unknown
// locale leaves English.
func Use(locale string) {
	t := map[string]string{}
	for _, name := range candidates(locale) {
		if b, err := files.ReadFile("locales/" + name + ".toml"); err == nil {
			_, _ = toml.Decode(string(b), &t)
			break
		}
	}
	mu.Lock()
	tab = t
	mu.Unlock()
}

func candidates(locale string) []string {
	if locale == "" {
		return nil
	}
	short, _, _ := strings.Cut(locale, "_")
	return []string{locale, short}
}

// T returns the translation of an English UI string, or the string itself.
func T(s string) string {
	mu.RLock()
	defer mu.RUnlock()
	if v := tab[s]; v != "" {
		return v
	}
	return s
}
