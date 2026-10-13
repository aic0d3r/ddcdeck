package automation

import (
	"os"
	"strings"
	"testing"

	"github.com/aic0d3r/ddcdeck/internal/notify"
)

func TestDefaultShortcutsStayOutOfTheFile(t *testing.T) {
	newEnv(t)
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Shortcut) == 0 || !c.Shortcut[0].Default {
		t.Fatalf("with no [[shortcut]] the built-in suggestions must show, got %+v", c.Shortcut)
	}
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "brightness set +10") {
		t.Errorf("saving must not turn suggestions into real config:\n%s", b)
	}
	// one shortcut of your own takes over completely
	if err := SaveConfig(Config{Shortcut: []Shortcut{{Keys: "F9", Action: "bluelight toggle"}}}); err != nil {
		t.Fatal(err)
	}
	c, _ = LoadConfig()
	if len(c.Shortcut) != 1 || c.Shortcut[0].Default {
		t.Errorf("a configured list replaces the suggestions, got %+v", c.Shortcut)
	}
}

func TestFireNotifiesAndRemembers(t *testing.T) {
	e, _, _ := newEnv(t)
	var seen []string
	notifyFn = func(summary, body string) { seen = append(seen, summary+"|"+body) }
	t.Cleanup(func() { notifyFn = notify.Send })
	writeProfile(t, "night", "[monitor.\"*\"]\nbrightness = 25\n")

	r := Rule{ID: "sunset|night|", When: "sunset", Apply: []string{"night"}}
	e.fire(r)
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "sunset → night|") {
		t.Fatalf("a fired rule must announce itself, got %v", seen)
	}
	if e.LastFired(r.ID) == "" {
		t.Fatal("the last fire must be remembered, that is what answers \"did it run?\"")
	}

	e.fire(Rule{ID: "app|night|", When: "app", Apply: []string{"night"}})
	if len(seen) != 1 {
		t.Errorf("app rules switch windows all day: they must stay quiet, got %v", seen)
	}

	// the stamp survives a daemon restart
	e2 := NewEngine(e.svc)
	e2.stateFile = e.stateFile
	e2.state = engineState{}
	e2.loadState()
	if e2.LastFired(r.ID) == "" {
		t.Error("last fire must survive a restart")
	}
}
