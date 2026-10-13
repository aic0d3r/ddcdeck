// Package shortcuts registers global keyboard shortcuts via the XDG
// GlobalShortcuts portal (org.freedesktop.portal.Desktop, Plasma 6) and
// delivers activations to the caller.
package shortcuts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalDest      = "org.freedesktop.portal.Desktop"
	portalPath      = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	requestRoot     = "/org/freedesktop/portal/desktop/request/"
	gsInterface     = "org.freedesktop.portal.GlobalShortcuts"
	requestIface    = "org.freedesktop.portal.Request"
	sessionIface    = "org.freedesktop.portal.Session"
	signalActivated = gsInterface + ".Activated"

	minPortalVersion = 2 // Activated carries options as of version 2
	bindTimeout      = 15 * time.Second
	cleanupTimeout   = 3 * time.Second
	activationBuf    = 16
)

var errClosed = errors.New("shortcuts: manager closed")

// busConn is the slice of *dbus.Conn this package needs; tests substitute
// their own implementation. All method calls go to the portal destination.
type busConn interface {
	AddMatchSignal(options ...dbus.MatchOption) error
	RemoveMatchSignal(options ...dbus.MatchOption) error
	Signal(ch chan<- *dbus.Signal)
	Names() []string
	Close() error
	call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call
	property(p string) (dbus.Variant, error)
}

// connAdapter adapts *dbus.Conn to busConn.
type connAdapter struct{ *dbus.Conn }

func (c connAdapter) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return c.Conn.Object(portalDest, path).CallWithContext(ctx, method, 0, args...)
}

func (c connAdapter) property(p string) (dbus.Variant, error) {
	return c.Conn.Object(portalDest, portalPath).GetProperty(p)
}

var _ busConn = connAdapter{}

// Shortcut is one bindable action; ID is what Activated reports, Description
// is shown to the user when assigning keys.
type Shortcut struct {
	ID          string
	Description string
	Preferred   string // suggested trigger in shortcuts-spec form, e.g. "LOGO+SHIFT+b"
}

// Manager talks to the org.freedesktop.portal.GlobalShortcuts interface.
// Bind is safe to call again after any failure: each attempt cleans up what
// the previous one left behind. Close stops all activity.
type Manager struct {
	activations chan string
	dial        func() (busConn, error)
	requestPath func(busConn, string) dbus.ObjectPath

	bindMu sync.Mutex // serializes Bind (and its cleanup) against itself

	mu           sync.Mutex
	conn         busConn
	ownsConn     bool
	sigCh        chan *dbus.Signal
	waiters      map[dbus.ObjectPath]chan *dbus.Signal
	session      dbus.ObjectPath
	sessionMatch []dbus.MatchOption
	closed       bool

	tokens atomic.Int64
	stop   chan struct{}
	done   chan struct{}
}

// New returns a Manager ready to Bind. The session bus connection is made
// lazily on the first Bind, so New always succeeds.
func New() *Manager {
	return newManager(func() (busConn, error) {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return nil, err
		}
		return connAdapter{conn}, nil
	})
}

func newManager(dial func() (busConn, error)) *Manager {
	return &Manager{
		activations: make(chan string, activationBuf),
		dial:        dial,
		requestPath: portalRequestPath,
		waiters:     map[dbus.ObjectPath]chan *dbus.Signal{},
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Activations returns shortcut IDs as the user presses them. Buffered; when
// the consumer falls behind, activations are dropped.
func (m *Manager) Activations() <-chan string { return m.activations }

// Bind (re-)binds the shortcuts. It creates a portal session and calls
// BindShortcuts, replacing any previous bindings. It blocks until the portal
// answers (max 15 s) and returns an error when the portal is missing or
// refuses; the Manager stays usable for another Bind attempt.
func (m *Manager) Bind(ctx context.Context, shortcuts []Shortcut) error {
	if len(shortcuts) == 0 {
		return errors.New("shortcuts: nothing to bind")
	}
	m.bindMu.Lock()
	defer m.bindMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, bindTimeout)
	defer cancel()

	if err := m.ensureConn(); err != nil {
		return err
	}
	if err := m.checkVersion(); err != nil {
		return err
	}
	m.releaseSession() // replace previous bindings, idempotent

	if err := m.createSession(ctx); err != nil {
		return err
	}
	return m.bindShortcuts(ctx, shortcuts)
}

// Close releases the session (best effort) and stops dispatching. It is
// idempotent and safe to call concurrently with Bind.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	conn, owns, session, match := m.conn, m.ownsConn, m.session, m.sessionMatch
	m.mu.Unlock()

	close(m.stop)
	if conn == nil {
		return
	}
	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := conn.call(ctx, session, sessionIface+".Close").Err; err != nil {
			slog.Debug("shortcuts: closing portal session", "err", err)
		}
	}
	if match != nil {
		if err := conn.RemoveMatchSignal(match...); err != nil {
			slog.Debug("shortcuts: removing session match", "err", err)
		}
	}
	if owns {
		if err := conn.Close(); err != nil {
			slog.Debug("shortcuts: closing bus connection", "err", err)
		}
	}
}

// ensureConn dials the bus on first use and starts the dispatch goroutine.
func (m *Manager) ensureConn() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errClosed
	}
	if m.conn != nil {
		return nil
	}
	conn, err := m.dial()
	if err != nil {
		return fmt.Errorf("shortcuts: connect to session bus: %w", err)
	}
	m.conn, m.ownsConn = conn, true
	m.sigCh = make(chan *dbus.Signal, 64)
	conn.Signal(m.sigCh)
	go m.dispatch()
	slog.Debug("shortcuts: connected to session bus", "name", conn.Names()[0])
	return nil
}

// checkVersion verifies the portal exposes GlobalShortcuts v2 or newer; a
// missing portal fails here quickly.
func (m *Manager) checkVersion() error {
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()
	v, err := conn.property(gsInterface + ".version")
	if err != nil {
		return fmt.Errorf("shortcuts: global shortcuts portal unavailable: %w", err)
	}
	version, ok := v.Value().(uint32)
	if !ok || version < minPortalVersion {
		return fmt.Errorf("shortcuts: GlobalShortcuts portal too old: version %v, need %d", v.Value(), minPortalVersion)
	}
	return nil
}

// releaseSession closes the current portal session, if any, and forgets it.
func (m *Manager) releaseSession() {
	m.mu.Lock()
	conn, session, match := m.conn, m.session, m.sessionMatch
	m.session, m.sessionMatch = "", nil
	m.mu.Unlock()
	if conn == nil || session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := conn.call(ctx, session, sessionIface+".Close").Err; err != nil {
		slog.Debug("shortcuts: closing replaced session", "err", err)
	}
	if err := conn.RemoveMatchSignal(match...); err != nil {
		slog.Debug("shortcuts: removing replaced session match", "err", err)
	}
}

// createSession runs the CreateSession request flow and stores the session.
func (m *Manager) createSession(ctx context.Context) error {
	n := m.tokens.Add(1)
	token := fmt.Sprintf("ddcdeck%d", n)
	options := map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(token),
		"session_handle_token": dbus.MakeVariant(fmt.Sprintf("s%d", n)),
	}

	m.mu.Lock()
	conn, rp := m.conn, m.requestPath
	m.mu.Unlock()
	reqPath := rp(conn, token)

	wait, cancelWait, err := m.subscribeRequest(conn, reqPath)
	if err != nil {
		return err
	}
	defer cancelWait()

	if call := conn.call(ctx, portalPath, gsInterface+".CreateSession", options); call.Err != nil {
		return fmt.Errorf("shortcuts: CreateSession: %w", call.Err)
	}
	sig, err := wait(ctx)
	if err != nil {
		return fmt.Errorf("shortcuts: CreateSession response: %w", err)
	}
	code, results, err := parseResponse(sig)
	if err != nil {
		return fmt.Errorf("shortcuts: CreateSession response: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("shortcuts: portal refused CreateSession: response %d", code)
	}
	handle, err := sessionHandle(results)
	if err != nil {
		return fmt.Errorf("shortcuts: CreateSession response: %w", err)
	}

	// One match on the session path covers Activated and Deactivated;
	// Deactivated is ignored by the dispatcher.
	match := sessionMatch(handle)
	if err := conn.AddMatchSignal(match...); err != nil {
		return fmt.Errorf("shortcuts: subscribe to session signals: %w", err)
	}
	m.mu.Lock()
	m.session, m.sessionMatch = handle, match
	m.mu.Unlock()
	slog.Info("shortcuts: portal session created", "session", string(handle))
	return nil
}

// bindShortcuts runs the BindShortcuts request flow for the current session.
func (m *Manager) bindShortcuts(ctx context.Context, list []Shortcut) error {
	n := m.tokens.Add(1)
	token := fmt.Sprintf("ddcdeck%d", n)
	options := map[string]dbus.Variant{"handle_token": dbus.MakeVariant(token)}
	// The portal takes a(sa{sv}): one (id, options) struct per shortcut with
	// "description" as a plain string; a map element or a map list would
	// serialize wrongly and be rejected.
	shortcuts := make([]portalShortcut, len(list))
	for i, s := range list {
		opts := map[string]dbus.Variant{"description": dbus.MakeVariant(s.Description)}
		if s.Preferred != "" { // desktops that support it pre-fill the key chooser
			opts["preferred_trigger"] = dbus.MakeVariant(s.Preferred)
		}
		shortcuts[i] = portalShortcut{s.ID, opts}
	}

	m.mu.Lock()
	conn, session, rp := m.conn, m.session, m.requestPath
	m.mu.Unlock()
	reqPath := rp(conn, token)

	wait, cancelWait, err := m.subscribeRequest(conn, reqPath)
	if err != nil {
		return err
	}
	defer cancelWait()

	call := conn.call(ctx, portalPath, gsInterface+".BindShortcuts", session, shortcuts, "", options)
	if call.Err != nil {
		return fmt.Errorf("shortcuts: BindShortcuts: %w", call.Err)
	}
	sig, err := wait(ctx)
	if err != nil {
		return fmt.Errorf("shortcuts: BindShortcuts response: %w", err)
	}
	code, _, err := parseResponse(sig)
	if err != nil {
		return fmt.Errorf("shortcuts: BindShortcuts response: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("shortcuts: portal refused BindShortcuts: response %d", code)
	}
	slog.Info("shortcuts: bound shortcuts", "count", len(list), "session", string(session))
	return nil
}

// subscribeRequest wires a waiter for the Request::Response signal expected
// on path; it must be called before the matching portal method call. cancel
// unregisters the waiter and its match rule.
func (m *Manager) subscribeRequest(conn busConn, path dbus.ObjectPath) (func(context.Context) (*dbus.Signal, error), func(), error) {
	match := []dbus.MatchOption{
		dbus.WithMatchSender(portalDest),
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(requestIface),
	}
	ch := make(chan *dbus.Signal, 1)
	m.mu.Lock()
	m.waiters[path] = ch
	m.mu.Unlock()
	if err := conn.AddMatchSignal(match...); err != nil {
		m.mu.Lock()
		delete(m.waiters, path)
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("shortcuts: subscribe to response on %s: %w", path, err)
	}
	cancel := func() {
		m.mu.Lock()
		delete(m.waiters, path)
		m.mu.Unlock()
		if err := conn.RemoveMatchSignal(match...); err != nil {
			slog.Debug("shortcuts: remove request match", "path", string(path), "err", err)
		}
	}
	wait := func(ctx context.Context) (*dbus.Signal, error) {
		select {
		case sig := <-ch:
			return sig, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return wait, cancel, nil
}

// dispatch is the dedicated signal reader; godbus delivers into the buffered
// sigCh, so the D-Bus dispatch is never blocked by this package.
func (m *Manager) dispatch() {
	defer close(m.done)
	for {
		select {
		case <-m.stop:
			return
		case sig, ok := <-m.sigCh:
			if !ok { // connection closed
				return
			}
			m.handleSignal(sig)
		}
	}
}

func (m *Manager) handleSignal(sig *dbus.Signal) {
	m.mu.Lock()
	ch := m.waiters[sig.Path]
	if ch != nil {
		delete(m.waiters, sig.Path)
	}
	session := m.session
	m.mu.Unlock()

	if ch != nil {
		select {
		case ch <- sig:
		default:
		}
		return
	}
	if sig.Name != signalActivated || sig.Path != session {
		return // Deactivated and anything else is ignored
	}
	if len(sig.Body) < 2 {
		return
	}
	id, ok := sig.Body[1].(string)
	if !ok {
		return
	}
	select {
	case m.activations <- id:
	default:
		slog.Warn("shortcuts: activation dropped, channel full", "id", id)
	}
}

// portalShortcut is the (id, extras) struct the BindShortcuts argument list
// expects on the wire.
type portalShortcut struct {
	ID     string
	Extras map[string]dbus.Variant
}

// parseResponse decodes a Request::Response body.
func parseResponse(sig *dbus.Signal) (uint32, map[string]dbus.Variant, error) {
	if len(sig.Body) < 2 {
		return 0, nil, fmt.Errorf("malformed response on %s: %d args", sig.Path, len(sig.Body))
	}
	code, ok := sig.Body[0].(uint32)
	if !ok {
		return 0, nil, fmt.Errorf("malformed response code on %s: %T", sig.Path, sig.Body[0])
	}
	results, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return 0, nil, fmt.Errorf("malformed response results on %s: %T", sig.Path, sig.Body[1])
	}
	return code, results, nil
}

// sessionHandle extracts results["session_handle"], accepting both the 'o'
// and 's' variant forms seen in the wild.
func sessionHandle(results map[string]dbus.Variant) (dbus.ObjectPath, error) {
	v, ok := results["session_handle"]
	if !ok {
		return "", errors.New("no session_handle in results")
	}
	switch h := v.Value().(type) {
	case dbus.ObjectPath:
		return h, nil
	case string:
		return dbus.ObjectPath(h), nil
	default:
		return "", fmt.Errorf("session_handle has unexpected type %T", h)
	}
}

func sessionMatch(handle dbus.ObjectPath) []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender(portalDest),
		dbus.WithMatchObjectPath(handle),
		dbus.WithMatchInterface(gsInterface),
	}
}

// portalRequestPath predicts the Request object path the portal derives from
// the caller's unique bus name and the handle token: the sender without
// colons (dots become underscores) followed by the token.
func portalRequestPath(conn busConn, token string) dbus.ObjectPath {
	sender := ""
	if names := conn.Names(); len(names) > 0 {
		sender = names[0]
	}
	return dbus.ObjectPath(requestRoot + strings.NewReplacer(":", "", ".", "_").Replace(sender) + "/" + token)
}
