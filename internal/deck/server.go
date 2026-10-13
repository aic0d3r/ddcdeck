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
		go func() { // a slow DDC call must not block other requests on this connection
			res, err := s.Handle(req.Method, req.Params)
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

// Client is a socket API client.
type Client struct {
	c   net.Conn
	sc  *bufio.Scanner
	id  int
	enc *json.Encoder
}

// Dial connects to the daemon.
func Dial(path string) (*Client, error) {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	return &Client{c: c, sc: sc, enc: json.NewEncoder(c)}, nil
}

func (cl *Client) Close() error { return cl.c.Close() }

// Call sends one request and decodes the result into out. Not for use after Subscribe.
func (cl *Client) Call(method string, params, out any) error {
	cl.id++
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := cl.enc.Encode(Request{ID: cl.id, Method: method, Params: raw}); err != nil {
		return err
	}
	for cl.sc.Scan() {
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *Error          `json:"error"`
		}
		if err := json.Unmarshal(cl.sc.Bytes(), &resp); err != nil || resp.ID != cl.id {
			continue // events or stale replies
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
	if err := cl.sc.Err(); err != nil {
		return err
	}
	return errors.New("daemon closed the connection")
}

// Events reads pushed events after a "subscribe" call.
func (cl *Client) Events(fn func(Event)) error {
	for cl.sc.Scan() {
		var e Event
		if json.Unmarshal(cl.sc.Bytes(), &e) == nil && e.Event != "" {
			fn(e)
		}
	}
	return cl.sc.Err()
}
