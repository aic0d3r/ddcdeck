package deck

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSocketRoundTrip(t *testing.T) {
	s, f := newTest(t)
	path := filepath.Join(t.TempDir(), "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, path, nil)

	var c *Client
	var err error
	for i := 0; i < 50 && c == nil; i++ {
		time.Sleep(10 * time.Millisecond)
		c, err = Dial(path)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var list ListResult
	if err := c.Call("list", nil, &list); err != nil || list.APIVersion != APIVersion || len(list.Monitors) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	events := make(chan Event, 8)
	if err := c.Subscribe(func(e Event) {
		select {
		case events <- e:
		default: // handlers must not block the reader
		}
	}); err != nil {
		t.Fatal(err)
	}
	var fv FeatureValue
	raw, _ := json.Marshal("off")
	if err := c.Call("set", Params{Feature: "bluelight", Value: raw}, &fv); err != nil || fv.Value != 0 {
		t.Fatalf("set: %+v %v", fv, err)
	}
	if e := <-events; e.Feature != "bluelight" || *e.Value != 0 {
		t.Fatalf("event: %+v", e)
	}
	var e *Error
	if err := c.Call("get", Params{Feature: "contrast"}, &fv); !errors.As(err, &e) || e.Code != ErrUnsupported {
		t.Fatalf("error code lost over the socket: %v", err)
	}

	// pipelined slider drag: the last value sent must be the one that sticks
	f.slow = true
	var waits []func() error
	for v := 1; v <= 30; v++ {
		waits = append(waits, c.Go("set", Params{Feature: "brightness", Value: json.RawMessage(strconv.Itoa(v))}, nil))
	}
	for _, w := range waits {
		if err := w(); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := f.vals[0x10].Cur; got != 30 || f.sets > 10 {
		t.Fatalf("final brightness %d after %d writes, want 30 after few", got, f.sets)
	}
}
