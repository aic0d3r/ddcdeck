// Package automation runs ddcdeck's profiles and rules: sun/time scheduling, hotplug
// reactions, the auto pause switch, and shortcut actions. It is pure config + math; all
// monitor access goes through the socket API methods it registers on deck.Service.
package automation

import (
	"math"
	"time"
)

// sunAngle is the elevation (degrees) below the horizon that counts as sunset/sunrise,
// matching the bluelight script this package replaces (refraction + solar disc).
const sunAngle = -0.833

// Elevation returns the approximate solar elevation in degrees (~0.5° accuracy, NOAA
// formula, no network). Ported from ~/.local/bin/bluelight.
func Elevation(lat, lon float64, t time.Time) float64 {
	t = t.UTC()
	g := 2 * math.Pi / 365 * (float64(t.YearDay()) - 1 + float64(t.Hour())/24)
	eqt := 229.18 * (0.000075 + 0.001868*math.Cos(g) - 0.032077*math.Sin(g) -
		0.014615*math.Cos(2*g) - 0.040849*math.Sin(2*g))
	decl := 0.006918 - 0.399912*math.Cos(g) + 0.070257*math.Sin(g) - 0.006758*math.Cos(2*g) +
		0.000907*math.Sin(2*g) - 0.002697*math.Cos(3*g) + 0.00148*math.Sin(3*g)
	ha := math.Pi / 180 * ((float64(t.Hour()*60+t.Minute())+eqt+4*lon)/4 - 180)
	la := math.Pi / 180 * lat
	return 180 / math.Pi * math.Asin(math.Sin(la)*math.Sin(decl)+math.Cos(la)*math.Cos(decl)*math.Cos(ha))
}

// IsNight reports whether the sun is below the sunset angle at t.
func IsNight(lat, lon float64, t time.Time) bool { return Elevation(lat, lon, t) < sunAngle }

// NextCrossing returns the next time after now when night == want, found by stepping
// coarsely (10 min) and refining (1 min). Max 2 days out (polar edge cases return zero time).
func NextCrossing(lat, lon float64, now time.Time, want bool) time.Time {
	limit := now.Add(48 * time.Hour)
	state := func(t time.Time) bool { return IsNight(lat, lon, t) }
	// leave the current state (if in it), then find the flip to the wanted one
	t := now.Add(10 * time.Minute)
	for ; t.Before(limit) && state(t) == want; t = t.Add(10 * time.Minute) {
	}
	for ; t.Before(limit) && state(t) != want; t = t.Add(10 * time.Minute) {
	}
	if !t.Before(limit) {
		return time.Time{} // polar day/night
	}
	lo, hi := t.Add(-10*time.Minute), t // refine: first minute where the state matches
	for hi.Sub(lo) > time.Minute {
		mid := lo.Add(hi.Sub(lo) / 2)
		if state(mid) == want {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}
