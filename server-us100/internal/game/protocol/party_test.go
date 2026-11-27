package protocol

import (
	"encoding/binary"
	"testing"
)

func TestSoloPartyUsesCurrentNativeRoster(t *testing.T) {
	const actor = 0x1234
	p, e := SoloPartyInfo(actor)
	if e != nil {
		t.Fatal(e)
	}
	if len(p) != 72 || binary.LittleEndian.Uint16(p) != 1 || binary.LittleEndian.Uint16(p[4:]) != 1 ||
		p[15] != 1 || p[47] != 1 || p[48] != 0 || binary.LittleEndian.Uint16(p[49:]) != actor {
		t.Fatalf("incorrect v2.24 local-party record %x", p)
	}
	for _, actor := range []uint16{0, 65535} {
		if _, e := SoloPartyInfo(actor); e == nil {
			t.Fatal("invalid actor accepted")
		}
	}
	pc, e := SoloPartyInfoContext(actor, [2]byte{1, 20})
	if e != nil {
		t.Fatal(e)
	}
	if pc[6] != 1 || pc[7] != 20 {
		t.Fatalf("context bytes = (%d, %d), want (1, 20)", pc[6], pc[7])
	}
}
