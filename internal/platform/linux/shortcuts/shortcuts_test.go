package shortcuts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeBus stands in for the session bus and the portal.
type fakeBus struct {
	mu             sync.Mutex
	sigCh          chan<- *dbus.Signal
	closed         bool
	version        uint32
	closedSessions []dbus.ObjectPath  // Session.Close calls, in order
	boundSessions  []dbus.ObjectPath  // session args of BindShortcuts
	bindCalls      [][]portalShortcut // shortcut lists received
	parentWindows  []string

	createRespond bool // emit a CreateSession Response at all
	createCode    uint32
	handleValue   dbus.Variant // session_handle as 'o' or 's'
	bindCode      uint32
	rp            func(token string) dbus.ObjectPath // request-path stub, shared with the Manager
}

func (f *fakeBus) AddMatchSignal(_ ...dbus.MatchOption) error    { return nil }
func (f *fakeBus) RemoveMatchSignal(_ ...dbus.MatchOption) error { return nil }
func (f *fakeBus) Names() []string                               { return []string{":1.42"} }
func (f *fakeBus) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeBus) Signal(ch chan<- *dbus.Signal) {
	f.mu.Lock()
	f.sigCh = ch
	f.mu.Unlock()
}

func (f *fakeBus) emit(sig *dbus.Signal) {
	f.mu.Lock()
	ch := f.sigCh
	f.mu.Unlock()
	if ch != nil {
		ch <- sig
	}
}

func (f *fakeBus) respond(path dbus.ObjectPath, code uint32, results map[string]dbus.Variant) {
	f.emit(&dbus.Signal{Path: path, Name: requestIface + ".Response", Body: []any{code, results}})
}

func (f *fakeBus) call(_ context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	switch method {
	case sessionIface + ".Close":
		f.mu.Lock()
		f.closedSessions = append(f.closedSessions, path)
		f.mu.Unlock()
		return &dbus.Call{}
	case gsInterface + ".CreateSession":
		token, _ := args[0].(map[string]dbus.Variant)["handle_token"].Value().(string)
		req := f.rp(token)
		f.mu.Lock()
		code, respond, handle := f.createCode, f.createRespond, f.handleValue
		f.mu.Unlock()
		if respond {
			go f.respond(req, code, map[string]dbus.Variant{"session_handle": handle})
		}
		return &dbus.Call{Body: []any{req}}
	case gsInterface + ".BindShortcuts":
		f.mu.Lock()
		f.boundSessions = append(f.boundSessions, args[0].(dbus.ObjectPath))
		f.bindCalls = append(f.bindCalls, args[1].([]portalShortcut))
		f.parentWindows = append(f.parentWindows, args[2].(string))
		code := f.bindCode
		f.mu.Unlock()
		token, _ := args[3].(map[string]dbus.Variant)["handle_token"].Value().(string)
		req := f.rp(token)
		go f.respond(req, code, map[string]dbus.Variant{"shortcuts": dbus.MakeVariant([]any{})})
		return &dbus.Call{Body: []any{req}}
	}
	return &dbus.Call{Err: errors.New("unexpected method " + method)}
}

func (f *fakeBus) property(p string) (dbus.Variant, error) {
	if p == gsInterface+".version" {
		f.mu.Lock()
		defer f.mu.Unlock()
		return dbus.MakeVariant(f.version), nil
	}
	return dbus.MakeVariant(0), errors.New("no such property " + p)
}

const testSession = "/org/freedesktop/portal/desktop/session/1_42/s1"

func newTestManager(t *testing.T) (*Manager, *fakeBus) {
	t.Helper()
	bus := &fakeBus{
		version:       minPortalVersion,
		createRespond: true,
		handleValue:   dbus.MakeVariant(dbus.ObjectPath(testSession)),
	}
	m := newManager(func() (busConn, error) { return bus, nil })
	rp := func(_ busConn, token string) dbus.ObjectPath {
		return dbus.ObjectPath(requestRoot + "1_42/" + token)
	}
	m.requestPath = rp
	bus.rp = func(token string) dbus.ObjectPath { return rp(nil, token) }
	t.Cleanup(m.Close)
	return m, bus
}

func recv[T any](t *testing.T, what string, ch <-chan T, d time.Duration) (T, bool) {
	t.Helper()
	select {
	case v := <-ch: // includes delivery from a closed channel (completion)
		return v, true
	case <-time.After(d):
		var zero T
		t.Logf("timed out after %v waiting for %s", d, what)
		return zero, false
	}
}

func testShortcuts() []Shortcut {
	return []Shortcut{
		{ID: "ddcdeck-selftest-up", Description: "Brightness up"},
		{ID: "ddcdeck-selftest-down", Description: "Brightness down"},
	}
}

func TestBindSessionHandleAsObjectPath(t *testing.T) {
	m, bus := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.bindCalls) != 1 {
		t.Fatalf("BindShortcuts called %d times, want 1", len(bus.bindCalls))
	}
	if got := bus.boundSessions[0]; dbus.ObjectPath(got) != dbus.ObjectPath(testSession) {
		t.Errorf("BindShortcuts session = %q, want %q", got, testSession)
	}
	if bus.parentWindows[0] != "" {
		t.Errorf("parent window = %q, want empty", bus.parentWindows[0])
	}
	call := bus.bindCalls[0]
	if len(call) != 2 {
		t.Fatalf("shortcut list has %d entries, want 2", len(call))
	}
	if call[0].ID != "ddcdeck-selftest-up" {
		t.Errorf("shortcut id = %q", call[0].ID)
	}
	desc, _ := call[0].Extras["description"].Value().(string)
	if desc != "Brightness up" {
		t.Errorf("shortcut description = %q", desc)
	}
}

func TestBindSessionHandleAsString(t *testing.T) {
	m, bus := newTestManager(t)
	bus.handleValue = dbus.MakeVariant(testSession) // 's' variant
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if got := bus.boundSessions[0]; dbus.ObjectPath(got) != dbus.ObjectPath(testSession) {
		t.Errorf("BindShortcuts session = %q, want %q", got, testSession)
	}
}

func TestActivationDelivery(t *testing.T) {
	m, bus := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	activated := &dbus.Signal{
		Path: testSession,
		Name: signalActivated,
		Body: []any{dbus.ObjectPath(testSession), "ddcdeck-selftest-up", uint64(time.Now().UnixMilli()), map[string]dbus.Variant{}},
	}
	go bus.emit(activated)
	id, ok := recv(t, "activation", m.Activations(), time.Second)
	if !ok || id != "ddcdeck-selftest-up" {
		t.Fatalf("activation = %q, %v; want ddcdeck-selftest-up, true", id, ok)
	}

	// Deactivated on the session path must be ignored.
	go bus.emit(&dbus.Signal{
		Path: testSession,
		Name: gsInterface + ".Deactivated",
		Body: []any{dbus.ObjectPath(testSession), "ddcdeck-selftest-up", uint64(1), map[string]dbus.Variant{}},
	})
	if _, ok := recv(t, "no delivery of Deactivated", m.Activations(), 150*time.Millisecond); ok {
		t.Error("Deactivated was delivered")
	}

	// Activated on a foreign path must be ignored.
	go bus.emit(&dbus.Signal{Path: "/some/other/path", Name: signalActivated,
		Body: []any{dbus.ObjectPath("/some/other/path"), "ddcdeck-selftest-x", uint64(2), map[string]dbus.Variant{}}})
	if _, ok := recv(t, "no delivery for foreign path", m.Activations(), 150*time.Millisecond); ok {
		t.Error("activation on foreign path was delivered")
	}
}

func TestBindRefusedThenRecovers(t *testing.T) {
	m, bus := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	bus.createCode = 1
	err := m.Bind(ctx, testShortcuts())
	if err == nil || !strings.Contains(err.Error(), "refused CreateSession") {
		t.Fatalf("CreateSession refusal: err = %v", err)
	}

	bus.createCode = 0
	bus.bindCode = 2
	err = m.Bind(ctx, testShortcuts())
	if err == nil || !strings.Contains(err.Error(), "refused BindShortcuts") {
		t.Fatalf("BindShortcuts refusal: err = %v", err)
	}

	// The Manager must still be usable after both failures.
	bus.bindCode = 0
	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("Bind after failures: %v", err)
	}
}

func TestBindPortalVersionTooOld(t *testing.T) {
	m, bus := newTestManager(t)
	bus.version = 1
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Bind(ctx, testShortcuts()); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("old portal version: err = %v", err)
	}
}

func TestBindTimeout(t *testing.T) {
	m, bus := newTestManager(t)
	bus.createRespond = false // portal never answers
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := m.Bind(ctx, testShortcuts())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: err = %v, want context deadline exceeded", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("Bind took %v, want it bounded by the ctx deadline", d)
	}

	// And the Manager recovers once the portal answers again.
	bus.createRespond = true
	bindCtx, bindCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer bindCancel()
	if err := m.Bind(bindCtx, testShortcuts()); err != nil {
		t.Fatalf("Bind after timeout: %v", err)
	}
}

func TestDropWhenFull(t *testing.T) {
	m, bus := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// 100 emissions into a 64-slot signal channel and a 16-slot activations
	// channel with no consumer: the emissions must complete (dispatch never
	// blocks) and exactly the first 16 activations survive.
	emitDone := make(chan struct{})
	go func() {
		defer close(emitDone)
		for i := 0; i < 100; i++ {
			bus.emit(&dbus.Signal{
				Path: testSession,
				Name: signalActivated,
				Body: []any{dbus.ObjectPath(testSession), fmt.Sprintf("ddcdeck-selftest-drop-%d", i), uint64(i), map[string]dbus.Variant{}},
			})
		}
	}()
	if _, ok := recv(t, "emissions to complete without a consumer", emitDone, 2*time.Second); !ok {
		t.Fatal("dispatch blocked: emissions stalled with no consumer")
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(m.sigCh) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < activationBuf; i++ {
		id, ok := recv(t, "activation", m.Activations(), time.Second)
		if !ok || id != fmt.Sprintf("ddcdeck-selftest-drop-%d", i) {
			t.Fatalf("activation %d = %q, %v", i, id, ok)
		}
	}
	if _, ok := recv(t, "no activation beyond buffer", m.Activations(), 150*time.Millisecond); ok {
		t.Error("more than activationBuf activations delivered")
	}
}

func TestRebindReplacesPreviousSession(t *testing.T) {
	m, bus := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("first Bind: %v", err)
	}
	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.closedSessions) != 1 {
		t.Errorf("Session.Close called %d times after rebind, want 1", len(bus.closedSessions))
	}
	if len(bus.bindCalls) != 2 {
		t.Errorf("BindShortcuts called %d times, want 2", len(bus.bindCalls))
	}
}

func TestCloseStopsDispatching(t *testing.T) {
	m, bus := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Bind(ctx, testShortcuts()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	m.Close()
	m.Close() // idempotent

	if _, ok := recv(t, "dispatch goroutine to stop", m.done, 2*time.Second); !ok {
		t.Fatal("dispatch goroutine did not stop")
	}
	bus.mu.Lock()
	closed, sessions := bus.closed, len(bus.closedSessions)
	bus.mu.Unlock()
	if !closed {
		t.Error("bus connection not closed")
	}
	if sessions != 1 {
		t.Errorf("Session.Close called %d times on Close, want 1", sessions)
	}

	// Bind after Close fails instead of touching the bus.
	if err := m.Bind(ctx, testShortcuts()); err == nil {
		t.Error("Bind after Close succeeded")
	}
}
