// Package ddc talks DDC/CI to monitors. v1 shells out to ddcutil (keeps libddcutil's GPL
// out of our binary and gets its per-monitor quirks for free).
package ddc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Value is a VCP reading. Max is 0 when the monitor doesn't report one. NC marks
// non-continuous codes (enum-like); the wizard uses it to infer the feature type.
type Value struct {
	Cur int  `json:"value"`
	Max int  `json:"max"`
	NC  bool `json:"nc,omitempty"`
}

// Display is one DDC-capable monitor found by Detect.
type Display struct {
	Bus       int
	Connector string // DRM connector without card prefix, e.g. "DP-8"
	Monitor   string // ddcutil's "MFG:MODEL:SERIAL", fallback when EDID is unreadable
}

// Caps maps each VCP code in the capabilities string to its listed values (may be empty).
type Caps map[byte][]int

// Backend is the only thing that touches I2C.
type Backend interface {
	Detect() ([]Display, error)
	Capabilities(bus int) (Caps, error)
	Get(bus int, codes ...byte) (map[byte]Value, error)
	Set(bus int, code byte, v int) error
}

// Ddcutil implements Backend with the ddcutil binary.
type Ddcutil struct{}

func run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "ddcutil", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String() + " " + out.String())
		err = fmt.Errorf("ddcutil %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return out.String(), err
}

func (Ddcutil) Detect() ([]Display, error) {
	out, err := run("detect", "--terse")
	ds := parseDetect(out)
	if len(ds) == 0 && err != nil { // exits 1 when any display is invalid (e.g. eDP), so only fail on no output
		return nil, err
	}
	return ds, nil
}

func (Ddcutil) Capabilities(bus int) (Caps, error) {
	out, err := run("--bus", strconv.Itoa(bus), "capabilities", "--terse")
	if err != nil {
		return nil, err
	}
	return parseCaps(out), nil
}

func (Ddcutil) Get(bus int, codes ...byte) (map[byte]Value, error) {
	args := []string{"--bus", strconv.Itoa(bus), "--noverify", "getvcp"}
	for _, c := range codes {
		args = append(args, fmt.Sprintf("%02X", c))
	}
	out, err := run(append(args, "--brief")...)
	vals := parseGet(out)
	if len(vals) == 0 && err != nil { // exits 1 if any code is unreadable; keep the ones that worked
		return nil, err
	}
	return vals, nil
}

func (Ddcutil) Set(bus int, code byte, v int) error {
	_, err := run("--bus", strconv.Itoa(bus), "--noverify", "setvcp", fmt.Sprintf("%02X", code), strconv.Itoa(v))
	return err
}

func parseDetect(out string) []Display {
	var ds []Display
	var cur *Display
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), ":")
		v = strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(line, "Display "):
			ds = append(ds, Display{})
			cur = &ds[len(ds)-1]
		case strings.HasPrefix(line, "Invalid display"):
			cur = nil
		case cur == nil:
		case k == "I2C bus":
			cur.Bus, _ = strconv.Atoi(strings.TrimPrefix(v, "/dev/i2c-"))
		case k == "DRM connector":
			if _, c, ok := strings.Cut(v, "-"); ok { // "card1-DP-8" → "DP-8"
				cur.Connector = c
			}
		case k == "Monitor":
			cur.Monitor = v
		}
	}
	return ds
}

// parseCaps reads the top-level codes of the vcp(...) group, e.g. "vcp(10 14(04 05) E6)".
func parseCaps(out string) Caps {
	caps := Caps{}
	i := strings.Index(out, "vcp(")
	if i < 0 {
		return caps
	}
	s := out[i+4:]
	var last byte
	depth := 0
	for _, f := range strings.FieldsFunc(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(s), func(r rune) bool { return r == ' ' }) {
		switch f {
		case "(":
			depth++
			continue
		case ")":
			if depth == 0 {
				return caps
			}
			depth--
			continue
		}
		n, err := strconv.ParseUint(f, 16, 16)
		if err != nil {
			continue
		}
		if depth == 0 {
			last = byte(n)
			caps[last] = nil
		} else if depth == 1 {
			caps[last] = append(caps[last], int(n))
		}
	}
	return caps
}

// parseGet reads --brief lines: "VCP 10 C 40 100", "VCP 60 SNC x1a", "VCP 14 CNC x00 x0f x00 x05".
func parseGet(out string) map[byte]Value {
	vals := map[byte]Value{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "VCP" {
			continue
		}
		code, err := strconv.ParseUint(f[1], 16, 8)
		if err != nil {
			continue
		}
		num := func(s string) int { // "x1a" is hex, "40" is decimal
			if h, ok := strings.CutPrefix(s, "x"); ok {
				n, _ := strconv.ParseInt(h, 16, 32)
				return int(n)
			}
			n, _ := strconv.Atoi(s)
			return n
		}
		switch {
		case f[2] == "C" && len(f) >= 5:
			vals[byte(code)] = Value{Cur: num(f[3]), Max: num(f[4])}
		case f[2] == "SNC":
			vals[byte(code)] = Value{Cur: num(f[3]), NC: true}
		case f[2] == "CNC" && len(f) >= 7:
			vals[byte(code)] = Value{Cur: num(f[5])<<8 | num(f[6]), Max: num(f[3])<<8 | num(f[4]), NC: true}
		}
	}
	return vals
}
