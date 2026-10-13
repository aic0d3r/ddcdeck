package automation

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCoord(t *testing.T) {
	for _, tc := range []struct {
		in       string
		lat, lon float64
	}{
		{"+5222+00454", 52 + 22.0/60, 4 + 54.0/60},
		{"-2332-04637", -(23 + 32.0/60), -(46 + 37.0/60)},
		{"+0130-08930", 1.5, -89.5},
		{"+5222.5+00454", 52 + 22.5/60, 4 + 54.0/60}, // fraction of the minutes
		{"+455230+0091735", 45 + 52.0/60 + 30.0/3600, 9 + 17.0/60 + 35.0/3600},
	} {
		lat, lon, ok := parseCoord(tc.in)
		if !ok || math.Abs(lat-tc.lat) > 1e-9 || math.Abs(lon-tc.lon) > 1e-9 {
			t.Errorf("parseCoord(%q) = (%.5f, %.5f, %v), want (%.5f, %.5f)", tc.in, lat, lon, ok, tc.lat, tc.lon)
		}
	}
	for _, bad := range []string{"", "junk", "+52+004", "+5222"} {
		if _, _, ok := parseCoord(bad); ok {
			t.Errorf("parseCoord(%q) accepted garbage", bad)
		}
	}
}

func TestLocationDefaultFromZoneTab(t *testing.T) {
	dir := t.TempDir()
	tab := filepath.Join(dir, "zone1970.tab")
	if err := os.WriteFile(tab, []byte("# comment\nNL\t+5222+00454\tEurope/Amsterdam\nBR\t-2332-04637\tAmerica/Sao_Paulo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := zoneTabPaths
	zoneTabPaths = []string{tab}
	t.Cleanup(func() { zoneTabPaths = old })

	t.Setenv("TZ", "America/Sao_Paulo")
	lat, lon := LocationDefault()
	if math.Abs(lat-(-(23+32.0/60))) > 1e-9 || math.Abs(lon-(-(46+37.0/60))) > 1e-9 {
		t.Errorf("Sao Paulo guessed as (%.4f, %.4f)", lat, lon)
	}

	t.Setenv("TZ", "Mars/Olympus_Mons") // no such zone: the documented last resort
	if lat, lon := LocationDefault(); lat != 52.37 || lon != 4.90 {
		t.Errorf("unknown zone must keep the old default, got (%.4f, %.4f)", lat, lon)
	}
}
