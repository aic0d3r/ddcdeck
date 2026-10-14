// Package ddc defines the platform-independent DDC/CI Backend interface and its types.
// Implementations live under internal/platform.
package ddc

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
