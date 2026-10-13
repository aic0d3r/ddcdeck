package deck

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// SocketPath is $XDG_RUNTIME_DIR/ddcdeck.sock.
func SocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "ddcdeck-"+strconv.Itoa(os.Getuid()))
		_ = os.MkdirAll(dir, 0o700)
	}
	return filepath.Join(dir, "ddcdeck.sock")
}

// Request / Response are one JSON line each.
type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     int    `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Serve listens on path (mode 0600) until ctx is done.
func (s *Service) Serve(ctx context.Context, path string) error {
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return errors.New("another ddcdeck daemon is already listening on " + path)
	}
	_ = os.Remove(path) // stale socket from a crash
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(c)
	}
}

func (s *Service) serveConn(c net.Conn) {
	defer c.Close()
	var wmu sync.Mutex
	enc := json.NewEncoder(c)
	send := func(v any) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = enc.Encode(v)
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			send(Response{Error: errf(ErrUsage, "bad request: %v", err)})
			continue
		}
		if req.Method == "subscribe" {
			// shortcut: subscribers get every event type; filter on params.events once the GUI needs it
			ch, cancel := s.Subscribe()
			defer cancel()
			send(Response{ID: req.ID, Result: map[string]bool{"subscribed": true}})
			go func() {
				for e := range ch {
					send(e)
				}
			}()
			continue
		}
		finish := s.Start(req.Method, req.Params) // in arrival order, so the last set wins
		go func() {                               // a slow DDC call must not block other requests
			res, err := finish()
			resp := Response{ID: req.ID, Result: res}
			if err != nil {
				var e *Error
				if !errors.As(err, &e) {
					e = errf(ErrDDC, "%v", err)
				}
				resp.Result, resp.Error = nil, e
			}
			send(resp)
		}()
	}
}

// WatchHotplug re-scans on DRM uevents (debounced; docks fire bursts) until ctx is done.
func (s *Service) WatchHotplug(ctx context.Context) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: 1}); err != nil {
		syscall.Close(fd)
		return err
	}
	go func() { <-ctx.Done(); syscall.Close(fd) }()
	kick := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-kick:
			}
			time.Sleep(1500 * time.Millisecond) // let MST settle and coalesce the burst
			for len(kick) > 0 {
				<-kick
			}
			if mons, err := s.Scan(); err != nil {
				slog.Warn("re-scan failed", "err", err)
			} else {
				slog.Info("re-scanned", "monitors", len(mons))
			}
		}
	}()
	buf := make([]byte, 16<<10)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if bytes.Contains(buf[:n], []byte("\x00SUBSYSTEM=drm\x00")) {
			select {
			case kick <- struct{}{}:
			default:
			}
		}
	}
}

// Notify sends an sd_notify state (e.g. "READY=1"); no-op outside systemd.
func Notify(state string) {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return
	}
	c, err := net.Dial("unixgram", addr)
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte(state))
}

// Client is a socket API client. Calls may run concurrently; events go to the Subscribe handler.
type Client struct {
	c       net.Conn
	mu      sync.Mutex
	id      int
	pending map[int]chan clientResp
	onEvent func(Event)
	done    chan struct{}
}

type clientResp struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
	Event  string          `json:"event"`
}

// Dial connects to the daemon.
func Dial(path string) (*Client, error) {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	cl := &Client{c: c, pending: map[int]chan clientResp{}, done: make(chan struct{})}
	go cl.readLoop()
	return cl, nil
}

func (cl *Client) readLoop() {
	defer close(cl.done)
	sc := bufio.NewScanner(cl.c)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r clientResp
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		cl.mu.Lock()
		if r.Event != "" {
			fn := cl.onEvent
			cl.mu.Unlock()
			var e Event
			if fn != nil && json.Unmarshal(sc.Bytes(), &e) == nil {
				fn(e)
			}
			continue
		}
		ch := cl.pending[r.ID]
		delete(cl.pending, r.ID)
		cl.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	}
}

// Done is closed when the connection drops.
func (cl *Client) Done() <-chan struct{} { return cl.done }

func (cl *Client) Close() error { return cl.c.Close() }

// Call sends one request and decodes the result into out (may be nil).
func (cl *Client) Call(method string, params, out any) error { return cl.Go(method, params, out)() }

// Go sends the request now (so requests reach the daemon in Go order) and returns a func
// that waits for the reply.
func (cl *Client) Go(method string, params, out any) func() error {
	raw, err := json.Marshal(params)
	if err != nil {
		return func() error { return err }
	}
	ch := make(chan clientResp, 1)
	cl.mu.Lock()
	cl.id++
	id := cl.id
	cl.pending[id] = ch
	b, _ := json.Marshal(Request{ID: id, Method: method, Params: raw})
	_, err = cl.c.Write(append(b, '\n'))
	cl.mu.Unlock()
	if err != nil {
		return func() error { return err }
	}
	return func() error { return cl.wait(ch, out) }
}

func (cl *Client) wait(ch chan clientResp, out any) error {
	select {
	case r := <-ch:
		if r.Error != nil {
			return r.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.Result, out)
	case <-cl.done:
		return errors.New("daemon closed the connection")
	}
}

// Subscribe routes pushed events to fn (called on the reader goroutine; don't block).
func (cl *Client) Subscribe(fn func(Event)) error {
	cl.mu.Lock()
	cl.onEvent = fn
	cl.mu.Unlock()
	return cl.Call("subscribe", nil, nil)
}
