package deck

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSocketRoundTrip(t *testing.T) {
	s, _ := newTest(t)
	path := filepath.Join(t.TempDir(), "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, path)

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
	var fv FeatureValue
	raw, _ := json.Marshal("off")
	if err := c.Call("set", Params{Feature: "bluelight", Value: raw}, &fv); err != nil || fv.Value != 0 {
		t.Fatalf("set: %+v %v", fv, err)
	}
	var e *Error
	if err := c.Call("get", Params{Feature: "contrast"}, &fv); !errors.As(err, &e) || e.Code != ErrUnsupported {
		t.Fatalf("error code lost over the socket: %v", err)
	}
}
