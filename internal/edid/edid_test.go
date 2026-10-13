package edid

import (
	"os"
	"testing"
)

func TestParsePA279CRV(t *testing.T) {
	b, err := os.ReadFile("testdata/pa279crv.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	want := Info{Mfg: "AUS", Product: 10209, Model: "PA279CRV", Serial: "TALMSB010776"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, err := Parse(b[:64]); err == nil {
		t.Fatal("short edid should fail")
	}
}
