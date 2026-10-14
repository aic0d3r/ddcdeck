package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndResolvePA279CRV(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	models, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	m := Match(models, "AUS", "PA279CRV", 10209)
	if m == nil {
		t.Fatal("PA279CRV not matched")
	}
	caps := map[byte][]int{0x10: nil, 0x14: {0x04, 0x05, 0x08, 0x0e}, 0x60: {0x0f, 0x11}}
	fs := Resolve(m, caps)
	if _, ok := fs["contrast"]; ok {
		t.Error("contrast not in caps, should be dropped")
	}
	if len(fs["colortemp"].Values) != 3 {
		t.Errorf("colortemp values = %v, want 5000k/6500k/9300k", fs["colortemp"].Values)
	}
	if fs["input"].Values["usbc"] != 0x1a {
		t.Error("model input values should replace defaults")
	}
	if b := fs["bluelight"]; b.VCP != 0xE6 || b.Off == nil || *b.Off != 0 || b.Max != 20 {
		t.Errorf("bluelight = %+v", b)
	}
	if Defaults()["colortemp"].Values["srgb"] == 0 {
		t.Error("Resolve must not mutate Defaults")
	}
}

func TestValidateRejectsEmptyNames(t *testing.T) {
	bad := Model{Mfg: "X", Model: "Y", Features: map[string]Feature{"in": {VCP: 0x60, Type: "enum", Values: map[string]int{"": 1}}}}
	if bad.validate() == nil {
		t.Fatal("empty value name accepted")
	}
}

func TestSaveUserModelMergeAndMCCS(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	off := 0
	first := Model{Mfg: "AUS", Model: "PA279CRV", Product: 10209, Features: map[string]Feature{
		"bluelight": {VCP: 0xE6, Type: "range", Max: 20, Off: &off, Verified: true},
	}}
	if _, err := SaveUserModel(first); err != nil {
		t.Fatal(err)
	}
	second := Model{Mfg: "AUS", Model: "PA279CRV", Product: 10209, Features: map[string]Feature{
		"kvm": {VCP: 0xF5, Type: "enum", Values: map[string]int{"usb1": 1}, Verified: true},
	}}
	path, err := SaveUserModel(second)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := LoadProfileModel(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := merged.Features["bluelight"]; !ok { // second discovery must keep the first
		t.Fatalf("merge lost bluelight: %+v", merged.Features)
	}
	mccs := merged.MCCS()
	for _, want := range []string{
		"MFG_ID       AUS", "FEATURE_CODE E6 bluelight", "ATTRS RW C",
		"FEATURE_CODE F5 kvm", "ATTRS RW NC", "VALUE x01 usb1", // names ddcutil shows for enum values
	} {
		if !strings.Contains(mccs, want) {
			t.Errorf("mccs missing %q:\n%s", want, mccs)
		}
	}
	// re-discovering a known feature must update it, not keep the stale code
	third := Model{Mfg: "AUS", Model: "PA279CRV", Product: 10209, Features: map[string]Feature{
		"bluelight": {VCP: 0xD0, Type: "range", Max: 10, Verified: true},
	}}
	if _, err := SaveUserModel(third); err != nil {
		t.Fatal(err)
	}
	merged, err = LoadProfileModel(path)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Features["bluelight"].VCP != 0xD0 {
		t.Fatalf("re-discovery must win: bluelight VCP = %#x", merged.Features["bluelight"].VCP)
	}
	if _, ok := merged.Features["kvm"]; !ok {
		t.Fatal("merge lost kvm")
	}
}

func TestVendorHints(t *testing.T) {
	hs := VendorHints("aus")
	if len(hs) == 0 {
		t.Fatal("no ASUS hints: vendors.toml missing or invalid")
	}
	for _, h := range hs {
		if h.VCP < 0xB0 && h.VCP != 0x60 || h.VCP > 0xff || h.Name == "" {
			t.Errorf("bad hint %+v", h)
		}
	}
	if len(VendorHints("DEL")) == 0 || len(VendorHints("GSM")) == 0 || len(VendorHints("ZZZ")) != 0 {
		t.Error("vendor lookup wrong")
	}
}

func TestMCCSSkipsWideValues(t *testing.T) {
	m := Model{Mfg: "A", Model: "B", Features: map[string]Feature{"p": {VCP: 0xE3, Type: "enum", Values: map[string]int{"x": 0x1ff}}}}
	if out := m.MCCS(); strings.Contains(out, "x1FF") || strings.Contains(out, " NC") {
		t.Errorf("wide value leaked:\n%s", out)
	}
}

func TestResolveDropsUnverifiedFeatureNotInCaps(t *testing.T) {
	m := &Model{Mfg: "X", Model: "Y", Features: map[string]Feature{
		"guess":  {VCP: 0xE1, Type: "range"},
		"proven": {VCP: 0xE2, Type: "range", Verified: true},
	}}
	fs := Resolve(m, map[byte][]int{0x10: nil})
	if _, ok := fs["guess"]; ok {
		t.Error("unverified feature outside caps must be dropped")
	}
	if _, ok := fs["proven"]; !ok {
		t.Error("verified feature must be kept")
	}
	if _, ok := Resolve(m, map[byte][]int{0xE1: nil})["guess"]; !ok {
		t.Error("unverified feature listed in caps must be kept")
	}
	if _, ok := Resolve(m, nil)["guess"]; !ok {
		t.Error("unknown caps (nil) keeps everything, as before")
	}
}

func TestUserModelMergesOverBundled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ddcdeck", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := "mfg=\"AUS\"\nmodel=\"PA279CRV\"\nproduct=10209\n[feature.preset]\nvcp=0xE3\ntype=\"range\"\nverified=true\n"
	if err := os.WriteFile(filepath.Join(dir, "ddcdeck", "models", "u.toml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	models, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	m := Match(models, "AUS", "PA279CRV", 10209)
	if m.Features["preset"].VCP != 0xE3 {
		t.Error("user feature must win")
	}
	if _, ok := m.Features["bluelight"]; !ok {
		t.Error("bundled bluelight must survive a partial user file")
	}
}
