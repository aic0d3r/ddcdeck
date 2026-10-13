// Package location resolves the user's approximate coordinates from their timezone:
// the zone's reference city, read from the system's own tzdata (zone.tab/zone1970.tab).
// Sun times are the same across a whole zone to within a few minutes, so this is a good
// default until a precise location is set.
package location

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// zone.tab lookup: the user's timezone names a city, and the system's own tzdata carries
// that city's coordinates. Sun times are the same across a whole zone to within a few
// minutes, so this is a good default until [location] is set precisely.
var zoneTabPaths = []string{
	"/usr/share/zoneinfo/zone1970.tab",
	"/usr/share/zoneinfo/zone.tab",
	"/usr/lib/zoneinfo/zone.tab", // Debian layout
}

// LocationDefault is the fallback when rules.toml has no [location]: the local timezone's
// reference city, else the migrated bluelight script's default.
func Default() (float64, float64) {
	if lat, lon, ok := tzLocation(localZone()); ok {
		return lat, lon
	}
	return 52.37, 4.90
}

// localZone returns the IANA zone name the system clock uses ("" when it can't tell).
func localZone() string {
	if tz := os.Getenv("TZ"); tz != "" && tz != "localtime" {
		return tz
	}
	p, err := filepath.EvalSymlinks("/etc/localtime")
	if err != nil {
		return ""
	}
	for _, base := range []string{"/usr/share/zoneinfo", "/usr/lib/zoneinfo", "/var/db/zoneinfo"} {
		if zone, ok := strings.CutPrefix(p, base+"/"); ok {
			return zone
		}
	}
	return ""
}

// tzLocation reads the zone's coordinates from zone.tab/zone1970.tab (CC, coords, name).
// zone1970.tab omits zones that share their clock with a bigger one since 1970 (e.g.
// Asia/Tokyo's entry lives only in zone.tab), so every file is tried until one has it.
func tzLocation(zone string) (float64, float64, bool) {
	if zone == "" {
		return 0, 0, false
	}
	for _, p := range zoneTabPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 && f[2] == zone {
				if lat, lon, ok := parseCoord(f[1]); ok {
					return lat, lon, true
				}
			}
		}
	}
	return 0, 0, false
}

// parseCoord reads zone.tab's ISO 6709 form, e.g. "+5222+00454" or "-2332-0463720":
// sign, degrees, then pairs of minutes/seconds, optionally a fraction on the last pair.
func parseCoord(c string) (lat, lon float64, ok bool) {
	if len(c) < 8 { // +DD+DDD is the shortest possible
		return 0, 0, false
	}
	lat, rest, ok := signedGroups(c, 2)
	if !ok {
		return 0, 0, false
	}
	lon, _, ok = signedGroups(rest, 3)
	return lat, lon, ok
}

func signedGroups(s string, deg int) (float64, string, bool) {
	if s == "" || (s[0] != '+' && s[0] != '-') {
		return 0, s, false
	}
	v, rest, ok := groups(s[1:], deg)
	if !ok {
		return 0, s, false
	}
	if s[0] == '-' {
		v = -v
	}
	return v, rest, true
}

// groups eats deg digits, then pairs, and returns degrees (+min/60 +sec/3600).
// It stops at the longitude's sign: that is where the next coordinate begins.
func groups(s string, deg int) (float64, string, bool) {
	var vals []float64
	take := deg
	for len(vals) < 3 {
		if len(s) < take || s[0] == '+' || s[0] == '-' || !isDigits(s[:take]) {
			break
		}
		v, err := strconv.ParseFloat(s[:take], 64)
		if err != nil {
			return 0, s, false
		}
		vals = append(vals, v)
		s = s[take:]
		take = 2
		if strings.HasPrefix(s, ".") { // fraction of the smallest unit so far
			i := 1
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			f, err := strconv.ParseFloat(s[:i], 64)
			if err != nil {
				return 0, s, false
			}
			vals[len(vals)-1] += f
			s = s[i:]
			break
		}
	}
	if len(vals) == 0 {
		return 0, s, false
	}
	out := vals[0]
	if len(vals) > 1 {
		out += vals[1] / 60
	}
	if len(vals) > 2 {
		out += vals[2] / 3600
	}
	return out, s, true
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
