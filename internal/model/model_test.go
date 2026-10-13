package model

import "testing"

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
