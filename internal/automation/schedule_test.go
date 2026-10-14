package automation

import (
	"testing"
	"time"
)

func TestBluelightNow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 14, h, m, 0, 0, time.Local) }
	span := "21:00-07:00"
	for _, c := range []struct {
		spec string
		h, m int
		want bool
	}{
		{span, 22, 0, true}, {span, 3, 0, true}, // inside, incl. past midnight
		{span, 12, 0, false}, {span, 20, 59, false},
		{"09:00-17:00", 12, 0, true}, {"09:00-17:00", 8, 0, false}, // daytime span
		{"off", 23, 0, false},
	} {
		if got := bluelightNow(Config{}, c.spec, at(c.h, c.m)); got != c.want {
			t.Errorf("bluelightNow(%q, %02d:%02d) = %v, want %v", c.spec, c.h, c.m, got, c.want)
		}
	}
	// sun variant: midnight is night, noon is day (Amsterdam-ish coords)
	cfg := Config{Location: struct {
		Lat float64 `toml:"lat"`
		Lon float64 `toml:"lon"`
	}{52.37, 4.9}}
	if !bluelightNow(cfg, "sunset", at(0, 30)) {
		t.Error("00:30 should be night")
	}
	if bluelightNow(cfg, "sunset", at(12, 0)) {
		t.Error("12:00 should be day")
	}
}
