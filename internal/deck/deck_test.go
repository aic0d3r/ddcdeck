package deck

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

type fake struct {
	mu   sync.Mutex
	vals map[byte]ddc.Value
	gets int
}

func (f *fake) Detect() ([]ddc.Display, error) {
	return []ddc.Display{{Bus: 21, Connector: "DP-8", Monitor: "AUS:PA279CRV:TALMSB010776"}}, nil
}
func (f *fake) Capabilities(int) (ddc.Caps, error) {
	return ddc.Caps{0x10: nil, 0x60: {0x0f, 0x11, 0x1a}, 0xE6: nil}, nil
}
func (f *fake) Get(_ int, codes ...byte) (map[byte]ddc.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	out := map[byte]ddc.Value{}
	for _, c := range codes {
		if v, ok := f.vals[c]; ok {
			out[c] = v
		}
	}
	return out, nil
}
func (f *fake) Set(_ int, c byte, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c == 0x99 {
		return errors.New("nak")
	}
	f.vals[c] = ddc.Value{Cur: v, Max: f.vals[c].Max}
	return nil
}

func newTest(t *testing.T) (*Service, *fake) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	models, err := model.Load()
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{vals: map[byte]ddc.Value{0x10: {Cur: 40, Max: 100}, 0x60: {Cur: 0x1a}, 0xE6: {Cur: 0, Max: 20}}}
	s := New(f, models)
	s.drmDir = t.TempDir() // no EDID: exercises the ddcutil fallback
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func call(t *testing.T, s *Service, method string, p Params) (FeatureValue, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	res, err := s.Handle(method, raw)
	fv, _ := res.(FeatureValue)
	return fv, err
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestSetSemantics(t *testing.T) {
	s, f := newTest(t)
	v := func(x string) json.RawMessage { b, _ := json.Marshal(x); return b }

	if fv, err := call(t, s, "set", Params{Feature: "bluelight", Value: v("toggle")}); err != nil || fv.Value != 20 || !*fv.On {
		t.Fatalf("toggle from off: %+v %v", fv, err)
	}
	if fv, _ := call(t, s, "set", Params{Feature: "bluelight", Value: v("toggle")}); fv.Value != 0 {
		t.Fatalf("toggle from on: %+v", fv)
	}
	if fv, _ := call(t, s, "set", Params{Feature: "brightness", Value: v("+70")}); fv.Value != 100 {
		t.Fatalf("relative should clamp: %+v", fv)
	}
	if fv, _ := call(t, s, "set", Params{Feature: "brightness", Value: json.RawMessage("30")}); f.vals[0x10].Cur != 30 || fv.Value != 30 {
		t.Fatalf("numeric json value: %+v", fv)
	}
	if fv, _ := call(t, s, "get", Params{Display: "PA279CRV", Feature: "input"}); fv.Name != "usbc" {
		t.Fatalf("enum name: %+v", fv)
	}
	if _, err := call(t, s, "set", Params{Feature: "input", Value: v("hdmi1")}); err != nil || f.vals[0x60].Cur != 0x11 {
		t.Fatalf("enum set: %v", err)
	}
	if _, err := call(t, s, "set", Params{Feature: "brightness", Value: v("101")}); code(err) != ErrUsage {
		t.Fatalf("out of range: %v", err)
	}
	if _, err := call(t, s, "get", Params{Feature: "contrast"}); code(err) != ErrUnsupported {
		t.Fatalf("contrast not in caps: %v", err)
	}
	if _, err := call(t, s, "get", Params{Display: "nope", Feature: "brightness"}); code(err) != ErrNoMonitor {
		t.Fatalf("bad display: %v", err)
	}
	raw, _ := json.Marshal(Params{Code: "99", Value: v("1")})
	if _, err := s.Handle("vcp.set", raw); code(err) != ErrDDC {
		t.Fatalf("ddc error: %v", err)
	}
}

func TestCacheAndEvents(t *testing.T) {
	s, f := newTest(t)
	ch, cancel := s.Subscribe()
	defer cancel()
	raw, _ := json.Marshal(Params{})
	if _, err := s.Handle("status", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Handle("status", raw); err != nil {
		t.Fatal(err)
	}
	if f.gets != 1 {
		t.Fatalf("status twice should hit I2C once (batched + cached), got %d", f.gets)
	}
	call(t, s, "set", Params{Feature: "brightness", Value: json.RawMessage("55")})
	if e := <-ch; e.Event != "value" || e.Feature != "brightness" || *e.Value != 55 {
		t.Fatalf("event: %+v", e)
	}
}
