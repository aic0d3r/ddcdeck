package wizard

import (
	"fmt"
	"sync"
	"testing"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
)

type fakeMon struct {
	mu   sync.Mutex
	vals map[byte]ddc.Value
	caps ddc.Caps
	gets int
}

func (f *fakeMon) Detect() ([]ddc.Display, error) { return nil, nil }
func (f *fakeMon) Capabilities(int) (ddc.Caps, error) {
	return f.caps, nil
}

func (f *fakeMon) caps14() {
	f.caps = ddc.Caps{0x10: nil, 0xE6: nil, 0x02: nil, 0x52: nil, 0x14: {4, 5, 8}}
}
func (f *fakeMon) Get(_ int, codes ...byte) (map[byte]ddc.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	out := map[byte]ddc.Value{}
	for _, c := range codes {
		if v, ok := f.vals[c]; ok {
			if c == 0x52 { // flaky code: hops between reads
				v.Cur = f.gets
			}
			out[c] = v
		}
	}
	return out, nil
}
func (f *fakeMon) Set(_ int, c byte, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[c] = ddc.Value{Cur: v, Max: f.vals[c].Max}
	return nil
}

func TestStartFiltersNoise(t *testing.T) {
	f := &fakeMon{caps: ddc.Caps{0x10: nil, 0xE6: nil, 0x02: nil, 0x52: nil}, vals: map[byte]ddc.Value{0x10: {Cur: 40, Max: 100}, 0xE6: {Cur: 0, Max: 20}, 0x02: {Cur: 5}, 0x52: {Cur: 1}}}
	s, err := Start(f, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range s.Codes() {
		if c == 0x02 || c == 0x52 {
			t.Fatalf("code %02X should have been filtered", c)
		}
	}
	if len(s.Codes()) != 2 { // 0x10 + 0xE6 only; E0–FF and other caps codes don't answer
		t.Fatalf("codes: % X", s.Codes())
	}
}

func TestDiffAndIntersect(t *testing.T) {
	f := &fakeMon{vals: map[byte]ddc.Value{0x10: {Cur: 40, Max: 100}, 0xE6: {Cur: 0, Max: 20}, 0x14: {Cur: 5, NC: true}}}
	f.caps14()
	s, _ := Start(f, 1)
	base := map[byte]ddc.Value{}
	for _, c := range s.Codes() {
		base[c] = map[byte]ddc.Value{0x10: {Cur: 40, Max: 100}, 0xE6: {Cur: 0, Max: 20}, 0x14: {Cur: 5, NC: true}}[c]
	}
	// user changes brightness and (annoyingly) an unrelated code
	f.vals[0x10] = ddc.Value{Cur: 55, Max: 100}
	f.vals[0x14] = ddc.Value{Cur: 8, NC: true}
	d1 := Changed(base, map[byte]ddc.Value{0x10: f.vals[0x10], 0x14: f.vals[0x14], 0xE6: {Cur: 0, Max: 20}})
	if len(d1) != 2 {
		t.Fatalf("diff: %v", d1)
	}
	// second change: only brightness moves. Consecutive diff (round1 → round2): the
	// unrelated code kept its new value, so it no longer appears between snapshots.
	f.vals[0x10] = ddc.Value{Cur: 60, Max: 100}
	snap1 := map[byte]ddc.Value{0x10: {Cur: 55, Max: 100}, 0x14: {Cur: 8, NC: true}, 0xE6: {Cur: 0, Max: 20}}
	d2 := Changed(snap1, map[byte]ddc.Value{0x10: f.vals[0x10], 0x14: f.vals[0x14], 0xE6: {Cur: 0, Max: 20}})
	if got := Intersect(d1, d2); len(got) != 1 || got[0x10][1].Cur != 60 {
		t.Fatalf("intersect: %v", got)
	}
}

func TestFeatureInference(t *testing.T) {
	r := Finding{Code: 0xE6, Old: ddc.Value{Cur: 0, Max: 20}, New: ddc.Value{Cur: 12, Max: 20}}
	fr := r.Feature(intPtr(0), nil)
	if fr.Type != "range" || fr.Max != 20 || *fr.Off != 0 {
		t.Fatalf("range: %+v", fr)
	}
	e := Finding{Code: 0x14, New: ddc.Value{Cur: 0x0f, NC: true}}
	fe := e.Feature(nil, map[string]int{"user1": 0x0f})
	if fe.Type != "enum" || fe.Values["user1"] != 0x0f {
		t.Fatalf("enum: %+v", fe)
	}
	if fmt.Sprint(sortedDiffCodes(map[byte][2]ddc.Value{0xE6: {}, 0x10: {}})) != "[10 E6]" {
		t.Fatal("sort")
	}
}

func intPtr(i int) *int { return &i }
