package gui

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/aic0d3r/ddcdeck/internal/deck"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

const noSelection = math.MaxUint32 // GTK_INVALID_LIST_POSITION

var titles = map[string]string{
	"brightness": "Brightness",
	"contrast":   "Contrast",
	"sharpness":  "Sharpness",
	"bluelight":  "Blue light filter",
	"colortemp":  "Color temperature",
	"preset":     "Picture preset",
	"input":      "Input source",
	"kvm":        "KVM / USB upstream",
}

func title(feature string) string {
	if t, ok := titles[feature]; ok {
		return t
	}
	return strings.ToUpper(feature[:1]) + strings.ReplaceAll(feature[1:], "_", " ")
}

var valueLabels = map[string]string{
	"dp": "DisplayPort", "dp1": "DisplayPort 1", "dp2": "DisplayPort 2", "usbc": "USB-C",
	"hdmi1": "HDMI 1", "hdmi2": "HDMI 2", "vga1": "VGA 1", "vga2": "VGA 2", "dvi1": "DVI 1", "dvi2": "DVI 2",
	"srgb": "sRGB", "native": "Native", "user1": "User 1", "user2": "User 2", "user3": "User 3",
	"adobergb": "Adobe RGB", "dci-p3": "DCI-P3", "rec709": "Rec. 709",
}

// valueLabel turns an enum key into display text: "6500k" → "6500 K", "hdmi1" → "HDMI 1".
func valueLabel(k string) string {
	if l, ok := valueLabels[k]; ok {
		return l
	}
	if n, ok := strings.CutSuffix(k, "k"); ok {
		if _, err := strconv.Atoi(n); err == nil {
			return n + " K"
		}
	}
	return strings.ToUpper(k[:1]) + k[1:]
}

// enumKeys sorts enum names by their VCP value (stable, matches the monitor's own order).
func enumKeys(f model.Feature) []string {
	ks := make([]string, 0, len(f.Values))
	for k := range f.Values {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return f.Values[ks[i]] < f.Values[ks[j]] })
	return ks
}

func setA11yLabel(w interface {
	UpdateProperty([]gtk.AccessibleProperty, []coreglib.Value)
}, label string) {
	w.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue(label)})
}

// newScale is a labelled 0..max slider with a numeric readout bound to a feature.
// The upper bound comes from the model file, else from the monitor's reported max.
func newScale(a *App, b *binder, feature string, f model.Feature) *gtk.Scale {
	hi := f.Max
	if hi == 0 {
		hi = 100 // replaced by the reported max on the first value
	}
	s := gtk.NewScaleWithRange(gtk.OrientationHorizontal, float64(f.Min), float64(hi), 1)
	s.SetDrawValue(true)
	s.SetValuePos(gtk.PosRight)
	s.SetDigits(0)
	s.SetRoundDigits(0)
	setA11yLabel(s, title(feature))
	last := -1
	s.ConnectValueChanged(func() {
		v := int(math.Round(s.Value()))
		if v != last {
			last = v
			a.set(feature, strconv.Itoa(v))
		}
	})
	b.on(feature, func(fv deck.FeatureValue) {
		if f.Max == 0 && fv.Max > 0 {
			s.SetRange(float64(f.Min), float64(fv.Max))
		}
		last = fv.Value
		s.SetValue(float64(fv.Value))
	})
	return s
}

// rangeRow is an action row with a slider suffix.
func rangeRow(a *App, b *binder, feature string, f model.Feature) *adw.ActionRow {
	r := adw.NewActionRow()
	r.SetTitle(title(feature))
	r.AddSuffix(newScale(a, b, feature, f))
	return r
}

// onOffRows is a switch plus a level slider, for range features with an "off" value.
func onOffRows(a *App, b *binder, feature string, f model.Feature) []gtk.Widgetter {
	sw := adw.NewSwitchRow()
	sw.SetTitle(title(feature))
	sw.Connect("notify::active", func() {
		a.set(feature, map[bool]string{true: "on", false: "off"}[sw.Active()])
	})
	b.on(feature, func(fv deck.FeatureValue) {
		if fv.On != nil {
			sw.SetActive(*fv.On)
		}
	})
	lvl := adw.NewActionRow()
	lvl.SetTitle("Level")
	lvl.SetSubtitle(strconv.Itoa(*f.Off) + " = off")
	sc := newScale(a, b, feature, f)
	setA11yLabel(sc, title(feature)+" level")
	lvl.AddSuffix(sc)
	return []gtk.Widgetter{sw, lvl}
}

// comboRow is a dropdown row for an enum feature. Unknown raw values show as no selection.
func comboRow(a *App, b *binder, feature string, f model.Feature) *adw.ComboRow {
	keys := enumKeys(f)
	labels := make([]string, len(keys))
	for i, k := range keys {
		labels[i] = valueLabel(k)
	}
	r := adw.NewComboRow()
	r.SetTitle(title(feature))
	r.SetModel(gtk.NewStringList(labels))
	r.SetSelected(noSelection)
	r.Connect("notify::selected", func() {
		if i := r.Selected(); int(i) < len(keys) {
			a.set(feature, keys[i])
		}
	})
	b.on(feature, func(fv deck.FeatureValue) {
		sel := uint(noSelection)
		for i, k := range keys {
			if k == fv.Name {
				sel = uint(i)
			}
		}
		r.SetSelected(sel)
		if sel == noSelection {
			r.SetSubtitle("Unknown value 0x" + strconv.FormatInt(int64(fv.Value), 16))
		} else {
			r.SetSubtitle("")
		}
	})
	return r
}

// featureRows picks the right control for any feature.
func featureRows(a *App, b *binder, feature string, f model.Feature) []gtk.Widgetter {
	switch {
	case f.Type == "enum":
		return []gtk.Widgetter{comboRow(a, b, feature, f)}
	case f.Off != nil:
		return onOffRows(a, b, feature, f)
	}
	return []gtk.Widgetter{rangeRow(a, b, feature, f)}
}

// group builds a preferences group with the given features (those the monitor supports).
// Returns nil when none are supported, so empty groups are never shown.
func group(a *App, b *binder, heading string, features ...string) *adw.PreferencesGroup {
	var g *adw.PreferencesGroup
	for _, name := range features {
		f, ok := a.cur.Features[name]
		if !ok {
			continue
		}
		if g == nil {
			g = adw.NewPreferencesGroup()
			g.SetTitle(heading)
		}
		for _, w := range featureRows(a, b, name, f) {
			g.Add(w)
		}
	}
	return g
}
