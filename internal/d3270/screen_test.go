package d3270

import (
	"strings"
	"testing"
)

func TestAddrRoundTrip(t *testing.T) {
	for _, addr := range []int{0, 1, 79, 80, 1919} {
		b0, b1 := EncodeAddr(addr)
		if got := DecodeAddr(b0, b1); got != addr {
			t.Errorf("addr %d round-tripped to %d", addr, got)
		}
	}
}

func TestEBCDICRoundTrip(t *testing.T) {
	for _, s := range []string{"HELLO", "Logon 3270", "user@host"} {
		if got := E2AString(A2EBytes(s)); got != s {
			t.Errorf("EBCDIC round-trip of %q = %q", s, got)
		}
	}
}

func TestScreenApplyEraseWrite(t *testing.T) {
	s := NewScreen("IBM-3278-2")
	// EW + WCC, SBA to (row 0, col 10), then "HELLO" in EBCDIC.
	b0, b1 := EncodeAddr(10)
	stream := []byte{CmdEW, 0xC3, OrderSBA, b0, b1}
	stream = append(stream, A2EBytes("HELLO")...)

	if err := s.Apply(stream); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out := s.Render()
	if !strings.Contains(out, "HELLO") {
		t.Errorf("rendered screen missing HELLO:\n%s", out)
	}
}

func TestScreenRepeatToAddress(t *testing.T) {
	s := NewScreen("IBM-3278-2")
	// SBA to 0, RA to address 5 filling EBCDIC '*'.
	sb0, sb1 := EncodeAddr(0)
	rb0, rb1 := EncodeAddr(5)
	stream := []byte{CmdW, 0xC3, OrderSBA, sb0, sb1, OrderRA, rb0, rb1, A2E('*')}
	if err := s.Apply(stream); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := s.Render(); !strings.Contains(got, "*****") {
		t.Errorf("expected 5 stars from RA order:\n%s", got)
	}
}

func TestGeometry(t *testing.T) {
	cases := map[string][2]int{
		"IBM-3278-2":   {24, 80},
		"IBM-3279-2-E": {24, 80},
		"IBM-3278-3":   {32, 80},
		"IBM-3278-4":   {43, 80},
		"IBM-3278-5":   {27, 132},
		"garbage":      {24, 80},
	}
	for model, want := range cases {
		r, c := Geometry(model)
		if r != want[0] || c != want[1] {
			t.Errorf("Geometry(%q) = %dx%d, want %dx%d", model, r, c, want[0], want[1])
		}
	}
}
