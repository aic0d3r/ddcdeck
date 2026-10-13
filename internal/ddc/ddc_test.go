package ddc

import (
	"reflect"
	"testing"
)

func TestParseDetect(t *testing.T) {
	out := `Invalid display
   I2C bus:          /dev/i2c-4
   DRM connector:    card1-eDP-1
   Monitor:          TMA:TL134ADXP03:

Display 1
   I2C bus:          /dev/i2c-21
   DRM connector:    card1-DP-8
   drm_connector_id: 485
   Monitor:          AUS:PA279CRV:TALMSB010776
`
	want := []Display{{Bus: 21, Connector: "DP-8", Monitor: "AUS:PA279CRV:TALMSB010776"}}
	if got := parseDetect(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseGet(t *testing.T) {
	out := "Feature 01 (Degauss) is not readable\nVCP 10 C 40 100\nVCP 60 SNC x1a\nVCP 14 CNC x00 x0f x00 x05\n"
	want := map[byte]Value{0x10: {Cur: 40, Max: 100}, 0x60: {Cur: 0x1a, NC: true}, 0x14: {Cur: 5, Max: 0x0f, NC: true}}
	if got := parseGet(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseCaps(t *testing.T) {
	out := "Unparsed capabilities string: (prot(monitor) cmds(01 02) vcp(02 10 14(04 05 08) 60(0F 11) E5(0003) E6 FF )mccs_ver(2.2))"
	got := parseCaps(out)
	want := Caps{0x02: nil, 0x10: nil, 0x14: {4, 5, 8}, 0x60: {0x0f, 0x11}, 0xE5: {3}, 0xE6: nil, 0xFF: nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
