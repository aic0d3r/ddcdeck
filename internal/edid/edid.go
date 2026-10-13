// Package edid parses the identity fields of a 128-byte EDID base block.
package edid

import (
	"bytes"
	"errors"
	"strings"
)

// Info is the monitor identity we key everything on.
type Info struct {
	Mfg     string // 3-letter PNP id, e.g. "AUS"
	Product int    // product code, e.g. 10209
	Model   string // monitor name descriptor (0xFC)
	Serial  string // serial descriptor (0xFF)
}

var header = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// Parse decodes mfg, product code, name and serial from an EDID.
func Parse(b []byte) (Info, error) {
	if len(b) < 128 || !bytes.Equal(b[:8], header) {
		return Info{}, errors.New("edid: missing or invalid header")
	}
	v := int(b[8])<<8 | int(b[9])
	info := Info{
		Mfg:     string([]byte{byte(v>>10&31) + '@', byte(v>>5&31) + '@', byte(v&31) + '@'}),
		Product: int(b[10]) | int(b[11])<<8,
	}
	for i := 54; i <= 108; i += 18 {
		d := b[i : i+18]
		if d[0] != 0 || d[1] != 0 {
			continue // detailed timing, not a text descriptor
		}
		text := strings.TrimSpace(strings.SplitN(string(d[5:]), "\n", 2)[0])
		switch d[3] {
		case 0xFC:
			info.Model = text
		case 0xFF:
			info.Serial = text
		}
	}
	return info, nil
}
