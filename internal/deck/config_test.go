package deck

import (
	"encoding/json"
	"testing"
)

func TestMatch(t *testing.T) {
	mons := []*Monitor{
		{Index: 1, ID: "AUS-X-S1", Model: "X", Serial: "S1", Connector: "DP-8"},
		{Index: 2, ID: "SAM-Y-S2", Model: "Y", Serial: "S2", Connector: "HDMI-1"},
	}
	for _, sel := range []string{"1", "2", "AUS-X-S1", "x", "s2", "hdmi-1"} {
		if Match(mons, sel) == nil {
			t.Errorf("Match(%q) found nothing", sel)
		}
	}
	for _, sel := range []string{"", "3", "nope"} {
		if Match(mons, sel) != nil {
			t.Errorf("Match(%q) must not guess", sel)
		}
	}
}

func TestDefaultDisplayPinning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTest(t) // one monitor: AUS-PA279CRV-TALMSB010776 on DP-8

	if err := SetDefaultDisplay("gone-docking-station"); err != nil {
		t.Fatal(err)
	}
	// the pinned monitor is not plugged in: fall back, never fail a command over it
	if fv, err := call(t, s, "get", Params{Feature: "brightness"}); err != nil || fv.Value != 40 {
		t.Errorf("a stale default must fall back to the first monitor, got %v / %v", fv, err)
	}
	if err := SetDefaultDisplay("DP-8"); err != nil {
		t.Fatal(err)
	}
	if DefaultDisplay() != "DP-8" {
		t.Errorf("default not persisted: %q", DefaultDisplay())
	}
	if fv, err := call(t, s, "get", Params{Feature: "brightness"}); err != nil || fv.Value != 40 {
		t.Errorf("pinned monitor not used: %v / %v", fv, err)
	}
	if err := SetDefaultDisplay(""); err != nil {
		t.Fatal(err)
	}
	if DefaultDisplay() != "" {
		t.Error("--clear must clear the value")
	}
}

func TestStatusFreshBypassesCache(t *testing.T) {
	s, f := newTest(t)
	status := func(p Params) {
		t.Helper()
		raw, _ := json.Marshal(p)
		if _, err := s.Handle("status", raw); err != nil {
			t.Fatal(err)
		}
	}
	before := f.gets
	status(Params{})
	status(Params{})
	if f.gets != before+1 {
		t.Errorf("two cached status calls should cost one I2C read, got %d", f.gets-before)
	}
	status(Params{Fresh: true})
	if f.gets != before+2 {
		t.Errorf("fresh: true must re-read the monitor (a change made on its OSD), got %d", f.gets-before)
	}
}
