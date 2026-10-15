// Package deck is the core shared by the daemon and the CLI fallback: monitor registry,
// per-bus serialized DDC access, read cache, and the JSON method dispatcher.
package deck

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aic0d3r/ddcdeck/internal/ddc"
	"github.com/aic0d3r/ddcdeck/internal/edid"
	"github.com/aic0d3r/ddcdeck/internal/model"
)

// APIVersion is bumped on breaking socket API changes.
const APIVersion = 1

const (
	cacheTTL     = 5 * time.Second // OSD changes show up after at most this long
	confirmDelay = 200 * time.Millisecond
)

// Error codes, mapped to CLI exit codes.
const (
	ErrUsage       = "usage"       // 1
	ErrNoMonitor   = "no_monitor"  // 2
	ErrUnsupported = "unsupported" // 3
	ErrDDC         = "ddc"         // 4
)

// Error is returned by Handle and carried over the socket.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func errf(code, format string, a ...any) *Error { return &Error{code, fmt.Sprintf(format, a...)} }

// Monitor is one connected DDC display, keyed by ID (MFG-MODEL-SERIAL), never by bus.
type Monitor struct {
	Index     int                      `json:"index"`
	ID        string                   `json:"id"`
	Mfg       string                   `json:"mfg"`
	Model     string                   `json:"model"`
	Product   int                      `json:"product"`
	Serial    string                   `json:"serial"`
	Connector string                   `json:"connector"`
	Bus       int                      `json:"bus"`
	HasModel  bool                     `json:"has_model_file"`
	Features  map[string]model.Feature `json:"features"`
	Caps      map[string][]int         `json:"caps,omitempty"` // VCP codes the monitor reports, hex keys
}

// Event is pushed to subscribers.
type Event struct {
	Event   string `json:"event"` // "value" | "hotplug" | "profile" | "rule" | "auto"
	Display string `json:"display"`
	Feature string `json:"feature,omitempty"`
	Profile string `json:"profile,omitempty"`
	Value   *int   `json:"value,omitempty"`
	State   string `json:"state,omitempty"` // hotplug: "connected" | "disconnected"
}

type wkey struct {
	id   string
	code byte
}

type cached struct {
	v  ddc.Value
	at time.Time
}

// Service owns DDC access. Safe for concurrent use.
type Service struct {
	be     ddc.Backend
	models []model.Model
	drmDir string

	mu      sync.Mutex
	mons    []*Monitor
	caps    map[string]ddc.Caps                           // by monitor ID, survives re-scans
	cache   map[string]map[byte]cached                    // by monitor ID
	buses   map[int]*sync.Mutex                           // one I2C transaction at a time per bus
	pending map[wkey]int                                  // latest value waiting to be written
	ext     map[string]func(json.RawMessage) (any, error) // methods added by automation
	writing map[wkey]bool                                 // a writer goroutine owns this key
	subs    map[chan Event]struct{}
}

// New creates a service; call Scan before use.
func New(be ddc.Backend, models []model.Model) *Service {
	return &Service{
		be: be, models: models, drmDir: "/sys/class/drm",
		caps: map[string]ddc.Caps{}, cache: map[string]map[byte]cached{},
		buses: map[int]*sync.Mutex{}, subs: map[chan Event]struct{}{},
		pending: map[wkey]int{}, writing: map[wkey]bool{},
	}
}

func (s *Service) bus(n int) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buses[n] == nil {
		s.buses[n] = &sync.Mutex{}
	}
	return s.buses[n]
}

// Scan re-detects monitors and emits hotplug events for changes. Returns the current list.
func (s *Service) Scan() ([]*Monitor, error) {
	if ms, err := model.Load(); err == nil || len(ms) > 0 {
		s.models = ms // re-read: wizard output and user files land without a daemon restart
	}
	ds, err := s.be.Detect()
	if err != nil {
		return nil, errf(ErrDDC, "%v", err)
	}
	var mons []*Monitor
	for i, d := range ds {
		m := s.identify(d)
		m.Index = i + 1
		s.mu.Lock()
		caps, ok := s.caps[m.ID]
		s.mu.Unlock()
		if !ok {
			l := s.bus(d.Bus)
			l.Lock()
			caps, err = s.be.Capabilities(d.Bus)
			l.Unlock()
			if err != nil {
				slog.Warn("capabilities unreadable, assuming all standard features", "display", m.ID, "err", err)
			}
			s.mu.Lock()
			s.caps[m.ID] = caps
			s.mu.Unlock()
		}
		mm := model.Match(s.models, m.Mfg, m.Model, m.Product)
		m.HasModel = mm != nil
		m.Features = model.Resolve(mm, caps)
		if len(caps) > 0 {
			m.Caps = map[string][]int{}
			for c, vs := range caps {
				m.Caps[fmt.Sprintf("%02x", c)] = vs
			}
		}
		mons = append(mons, m)
	}

	s.mu.Lock()
	old := s.mons
	s.mons = mons
	s.mu.Unlock()
	has := func(list []*Monitor, id string) bool {
		return slices.ContainsFunc(list, func(m *Monitor) bool { return m.ID == id })
	}
	for _, m := range mons {
		if !has(old, m.ID) {
			s.emit(Event{Event: "hotplug", Display: m.ID, State: "connected"})
		}
	}
	for _, m := range old {
		if !has(mons, m.ID) {
			s.emit(Event{Event: "hotplug", Display: m.ID, State: "disconnected"})
		}
	}
	return mons, nil
}

func (s *Service) identify(d ddc.Display) *Monitor {
	m := &Monitor{Connector: d.Connector, Bus: d.Bus}
	matches, _ := filepath.Glob(filepath.Join(s.drmDir, "card*-"+d.Connector, "edid"))
	for _, p := range matches {
		if b, err := os.ReadFile(p); err == nil {
			if info, err := edid.Parse(b); err == nil {
				m.Mfg, m.Model, m.Product, m.Serial = info.Mfg, info.Model, info.Product, info.Serial
				break
			}
		}
	}
	if m.Mfg == "" { // EDID unreadable: fall back to ddcutil's MFG:MODEL:SERIAL
		p := strings.SplitN(d.Monitor, ":", 3)
		for len(p) < 3 {
			p = append(p, "")
		}
		m.Mfg, m.Model, m.Serial = p[0], p[1], p[2]
	}
	m.ID = m.Mfg + "-" + m.Model
	if m.Serial != "" {
		m.ID += "-" + m.Serial
	} else {
		m.ID += "-bus" + strconv.Itoa(d.Bus) // shortcut: serial-less monitors aren't stable across re-docks, fine until someone has one
	}
	return m
}

// find resolves a display selector: "" (the pinned default, else the first), 1-based
// index, ID, model, serial or connector.
func (s *Service) find(sel string) (*Monitor, error) {
	pinned := sel == ""
	if pinned {
		sel = DefaultDisplay() // read outside the lock: it touches the disk
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mons) == 0 {
		return nil, errf(ErrNoMonitor, "no DDC monitor found")
	}
	if m := Match(s.mons, sel); m != nil {
		return m, nil
	}
	if pinned { // the pinned monitor is gone (undocked): fall back instead of failing
		return s.mons[0], nil
	}
	return nil, errf(ErrNoMonitor, "no monitor matches %q", sel)
}

// read returns cached values, fetching missing/stale codes in one batched ddcutil call.
func (s *Service) read(m *Monitor, fresh bool, codes ...byte) (map[byte]ddc.Value, error) {
	out := map[byte]ddc.Value{}
	var miss []byte
	s.mu.Lock()
	for _, c := range codes {
		if e, ok := s.cache[m.ID][c]; ok && !fresh && time.Since(e.at) < cacheTTL {
			out[c] = e.v
		} else {
			miss = append(miss, c)
		}
	}
	s.mu.Unlock()
	if len(miss) == 0 {
		return out, nil
	}
	l := s.bus(m.Bus)
	l.Lock()
	vals, err := s.be.Get(m.Bus, miss...)
	l.Unlock()
	if err != nil {
		return nil, errf(ErrDDC, "%v", err)
	}
	for c, v := range vals {
		s.store(m, c, v)
		out[c] = v
	}
	return out, nil
}

func (s *Service) store(m *Monitor, c byte, v ddc.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache[m.ID] == nil {
		s.cache[m.ID] = map[byte]cached{}
	}
	s.cache[m.ID][c] = cached{v, time.Now()}
}

func (s *Service) readOne(m *Monitor, code byte) (ddc.Value, error) {
	vals, err := s.read(m, false, code)
	if err != nil {
		return ddc.Value{}, err
	}
	v, ok := vals[code]
	if !ok {
		return v, errf(ErrDDC, "VCP %02X not readable on %s", code, m.ID)
	}
	return v, nil
}

// enqueue records v as the latest value for a VCP code: cache + event at once (optimistic),
// in call order. It returns the I2C part. Writes to the same code coalesce (latest wins), so a
// dragged slider costs a few I2C writes, not one per step; if another writer is already
// draining this code, the returned func is a no-op. After the last write, one confirming
// re-read emits a correction if the monitor disagrees.
func (s *Service) enqueue(m *Monitor, feature string, code byte, v int) func() error {
	k := wkey{m.ID, code}
	s.mu.Lock()
	prevMax := s.cache[m.ID][code].v.Max
	s.pending[k] = v
	busy := s.writing[k]
	s.writing[k] = true
	s.mu.Unlock()
	s.store(m, code, ddc.Value{Cur: v, Max: prevMax})
	if feature != "" {
		s.emit(Event{Event: "value", Display: m.ID, Feature: feature, Value: &v})
	}
	if busy {
		return func() error { return nil } // the running writer picks up our value
	}
	return func() error { return s.drain(m, feature, k, v) }
}

func (s *Service) drain(m *Monitor, feature string, k wkey, last int) error {
	code := k.code
	var first error
	for i := 0; ; i++ {
		s.mu.Lock()
		next, ok := s.pending[k]
		delete(s.pending, k)
		if !ok {
			delete(s.writing, k)
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		l := s.bus(m.Bus)
		l.Lock()
		err := s.be.Set(m.Bus, code, next)
		l.Unlock()
		last = next
		if err != nil {
			slog.Warn("vcp write failed", "display", m.ID, "code", fmt.Sprintf("%02X", code), "value", next, "err", err)
			s.mu.Lock()
			delete(s.cache[m.ID], code)
			s.mu.Unlock()
			if i == 0 {
				first = errf(ErrDDC, "%v", err)
			}
		}
	}
	go func() {
		time.Sleep(confirmDelay)
		vals, err := s.read(m, true, code)
		if got, ok := vals[code]; err == nil && ok && got.Cur != last && feature != "" {
			s.emit(Event{Event: "value", Display: m.ID, Feature: feature, Value: &got.Cur})
		}
	}()
	return first
}

// FeatureValue is the result of get/set.
type FeatureValue struct {
	Feature string `json:"feature"`
	Value   int    `json:"value"`
	Max     int    `json:"max"`
	Name    string `json:"name,omitempty"` // enum: friendly name of the value, if known
	On      *bool  `json:"on,omitempty"`   // range with an off value
}

// Present turns a raw value into a FeatureValue (enum name, on/off).
func Present(name string, f model.Feature, v ddc.Value) FeatureValue {
	fv := FeatureValue{Feature: name, Value: v.Cur, Max: v.Max}
	if f.Max > 0 {
		fv.Max = f.Max
	}
	for k, code := range f.Values {
		if code == v.Cur {
			fv.Name = k
		}
	}
	if f.Off != nil {
		on := v.Cur != *f.Off
		fv.On = &on
	}
	return fv
}

func (s *Service) feature(m *Monitor, name string) (model.Feature, error) {
	f, ok := m.Features[name]
	if !ok {
		return f, errf(ErrUnsupported, "%s does not support %s", m.ID, name)
	}
	return f, nil
}

// target turns a user value into a raw VCP value. readCur is only called when needed.
func target(f model.Feature, in string, readCur func() (ddc.Value, error)) (int, error) {
	in = strings.ToLower(strings.TrimSpace(in))
	if f.Type == "enum" {
		if v, ok := f.Values[in]; ok {
			return v, nil
		}
		if n, err := strconv.ParseInt(in, 0, 32); err == nil && n >= 0 && n <= 0xffff {
			return int(n), nil
		}
		return 0, errf(ErrUsage, "unknown value %q, expected one of: %s", in, strings.Join(slices.Sorted(maps.Keys(f.Values)), ", "))
	}
	lo, hi := f.Min, f.Max
	needCur := hi == 0 || in == "toggle" || strings.HasPrefix(in, "+") || strings.HasPrefix(in, "-")
	var cur ddc.Value
	if needCur {
		var err error
		if cur, err = readCur(); err != nil {
			return 0, err
		}
		if hi == 0 {
			hi = cur.Max
		}
	}
	on := hi
	if f.On != nil {
		on = *f.On
	}
	switch {
	case in == "on" || in == "off" || in == "toggle":
		if f.Off == nil {
			return 0, errf(ErrUsage, "this feature has no on/off")
		}
		if in == "off" || (in == "toggle" && cur.Cur != *f.Off) {
			return *f.Off, nil
		}
		return on, nil
	case strings.HasPrefix(in, "+") || strings.HasPrefix(in, "-"):
		d, err := strconv.Atoi(in)
		if err != nil {
			return 0, errf(ErrUsage, "bad value %q", in)
		}
		return min(max(cur.Cur+d, lo), hi), nil
	}
	n, err := strconv.Atoi(in)
	if err != nil || n < lo || n > hi {
		extra := ""
		if f.Off != nil {
			extra = ", on, off or toggle"
		}
		return 0, errf(ErrUsage, "value must be %d-%d, +N, -N%s", lo, hi, extra)
	}
	return n, nil
}

// Params is the union of all method parameters.
type Params struct {
	Display string          `json:"display,omitempty"`
	Feature string          `json:"feature,omitempty"`
	Code    string          `json:"code,omitempty"` // vcp.*: hex, e.g. "E6"
	Value   json.RawMessage `json:"value,omitempty"`
	Fresh   bool            `json:"fresh,omitempty"` // status: bypass the cache (a change made in the monitor's OSD)
}

// ListResult is the result of "list".
type ListResult struct {
	APIVersion int        `json:"api_version"`
	Monitors   []*Monitor `json:"monitors"`
}

// StatusResult is the result of "status".
type StatusResult struct {
	Display string         `json:"display"`
	Values  []FeatureValue `json:"values"`
}

// Handle dispatches one API method. "subscribe" is handled by the socket server.
func (s *Service) Handle(method string, raw json.RawMessage) (any, error) {
	return s.Start(method, raw)()
}

// Start runs the order-sensitive part of a request now (for writes: queueing the value) and
// returns the rest, which may block on I2C. Call Start in arrival order, finish concurrently.
func (s *Service) Start(method string, raw json.RawMessage) func() (any, error) {
	if method != "set" && method != "vcp.set" {
		return func() (any, error) { return s.handle(method, raw, nil) }
	}
	var wait func() error
	res, err := s.handle(method, raw, &wait)
	return func() (any, error) {
		if err == nil && wait != nil {
			err = wait()
		}
		if err != nil {
			return nil, err
		}
		return res, nil
	}
}

// handle does the work; set/vcp.set store their I2C step in *wait instead of running it.
func (s *Service) handle(method string, raw json.RawMessage, wait *func() error) (any, error) {
	var p Params
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errf(ErrUsage, "bad params: %v", err)
		}
	}
	if method == "list" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return ListResult{APIVersion, s.mons}, nil
	}
	if fn, ok := s.ext[method]; ok {
		return fn(raw)
	}
	m, err := s.find(p.Display)
	if err != nil {
		return nil, err
	}
	switch method {
	case "monitor": // the monitor a selector resolves to, with its features
		return m, nil
	case "scan": // re-detect (also reloads model files, e.g. after the wizard saved one)
		mons, err := s.Scan()
		if err != nil {
			return nil, err
		}
		return ListResult{APIVersion, mons}, nil
	case "status":
		var codes []byte
		for name, f := range m.Features {
			if name == "reset" {
				continue // write-only: many monitors reject reading it, failing the whole batch
			}
			codes = append(codes, byte(f.VCP))
		}
		vals, err := s.read(m, p.Fresh, codes...)
		if err != nil {
			return nil, err
		}
		res := StatusResult{Display: m.ID, Values: []FeatureValue{}}
		for _, name := range slices.Sorted(maps.Keys(m.Features)) {
			if v, ok := vals[byte(m.Features[name].VCP)]; ok {
				res.Values = append(res.Values, Present(name, m.Features[name], v))
			}
		}
		return res, nil
	case "get":
		f, err := s.feature(m, p.Feature)
		if err != nil {
			return nil, err
		}
		v, err := s.readOne(m, byte(f.VCP))
		if err != nil {
			return nil, err
		}
		return Present(p.Feature, f, v), nil
	case "set":
		f, err := s.feature(m, p.Feature)
		if err != nil {
			return nil, err
		}
		v, err := target(f, valueString(p.Value), func() (ddc.Value, error) { return s.readOne(m, byte(f.VCP)) })
		if err != nil {
			return nil, err
		}
		*wait = s.enqueue(m, p.Feature, byte(f.VCP), v)
		return Present(p.Feature, f, ddc.Value{Cur: v, Max: f.Max}), nil
	case "vcp.get", "vcp.set":
		code, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(p.Code), "0x"), 16, 8)
		if err != nil {
			return nil, errf(ErrUsage, "bad VCP code %q", p.Code)
		}
		if method == "vcp.get" {
			vals, err := s.read(m, true, byte(code))
			if err != nil {
				return nil, err
			}
			v, ok := vals[byte(code)]
			if !ok {
				return nil, errf(ErrUnsupported, "VCP %02X not readable", code)
			}
			return v, nil
		}
		n, err := strconv.ParseInt(valueString(p.Value), 0, 32)
		if err != nil || n < 0 || n > 0xffff {
			return nil, errf(ErrUsage, "value must be 0-65535")
		}
		*wait = s.enqueue(m, "", byte(code), int(n))
		return ddc.Value{Cur: int(n)}, nil
	}
	return nil, errf(ErrUsage, "unknown method %q", method)
}

// valueString accepts a JSON number or string.
func valueString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// Subscribe returns a channel of events; call cancel when done.
func (s *Service) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *Service) emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default: // slow subscriber drops events rather than blocking DDC
		}
	}
}

// Emit publishes an event to all subscribers (used by automation for profile/rule/auto events).
func (s *Service) Emit(e Event) { s.emit(e) }

// Extend registers an extra API method, so packages that import deck (automation) can add
// methods without deck importing them back.
func (s *Service) Extend(method string, fn func(raw json.RawMessage) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ext == nil {
		s.ext = map[string]func(json.RawMessage) (any, error){}
	}
	s.ext[method] = fn
}
