// Package location resolves the user's approximate coordinates from their timezone:
// the zone's reference city, read from the system's own tzdata (zone.tab/zone1970.tab).
// Sun times are the same across a whole zone to within a few minutes, so this is a good
// default until a precise location is set.
package location

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// zone.tab lookup: the user's timezone names a city, and the system's own tzdata carries
// that city's coordinates. Sun times are the same across a whole zone to within a few
// minutes, so this is a good default until [location] is set precisely.
var zoneTabPaths = []string{
	"/usr/share/zoneinfo/zone.tab",
	"/usr/share/zoneinfo/zone1970.tab",
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

// Zone is one tzdata line: an IANA zone, its reference city, and coordinates.
type Zone struct {
	Zone string  `json:"zone"`
	City string  `json:"city,omitempty"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

var allZones = func() []Zone {
	seen := map[string]bool{}
	var out []Zone
	for _, p := range zoneTabPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			// "CC coords zone comments..." (comments may contain spaces; some
			// distros ship these space-separated rather than tab-separated)
			if strings.HasPrefix(line, "#") {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			zone := f[2]
			if seen[zone] {
				continue
			}
			lat, lon, ok := parseCoord(f[1])
			if !ok {
				continue
			}
			seen[zone] = true
			city := ""
			if len(f) > 3 {
				city = strings.ReplaceAll(strings.Join(f[3:], " "), "_", " ")
			}
			out = append(out, Zone{Zone: zone, City: city, Lat: lat, Lon: lon})
		}
	}
	return out
}()

// Zones returns every tzdata zone with its reference city — the picker list.
func Zones() []Zone { return allZones }

// Find resolves free text to a zone: a typed city ("Osaka"), a zone ("Europe/Amsterdam"
// or just "amst"), or plain "lat lon" numbers. Best match wins; callers show nothing on
// failure rather than guessing.
func Find(query string) (Zone, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Zone{}, false
	}
	if lat, lon, ok := parseNumbers(q); ok {
		return Zone{Zone: q, City: q, Lat: lat, Lon: lon}, true
	}
	var best Zone
	score := 0
	for _, z := range allZones {
		city, zone := strings.ToLower(z.City), strings.ToLower(z.Zone)
		tail := zone[strings.LastIndex(zone, "/")+1:]
		s := 0
		switch {
		case city == q || zone == q || tail == q:
			s = 6
		case strings.HasPrefix(city, q) || strings.HasPrefix(tail, q):
			s = 4
		case strings.Contains(zone, q) || strings.Contains(city, q):
			s = 2
		}
		// a zone named after its city wins over one that merely mentions the text
		if s >= 2 && city == tail {
			s++
		}
		if s > score {
			best, score = z, s
		}
	}
	return best, score > 0
}

// Name labels coordinates with the nearest zone ("Amsterdam" for Rotterdam), "" when
// nothing is close enough to claim. Degree-distance with the usual cosine shrink.
func Name(lat, lon float64) string {
	best, dist := "", 1.5*1.5
	for _, z := range allZones {
		dy := z.Lat - lat
		dx := (z.Lon - lon) * cosd(lat)
		if d := dx*dx + dy*dy; d < dist {
			best, dist = z.Zone, d
		}
	}
	if best == "" {
		return ""
	}
	return best[strings.LastIndex(best, "/")+1:]
}

func cosd(deg float64) float64 {
	return math.Cos(deg * math.Pi / 180)
}

func parseNumbers(s string) (float64, float64, bool) {
	s = strings.ReplaceAll(s, ",", " ")
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(f[0], 64)
	lon, err2 := strconv.ParseFloat(f[1], 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}
