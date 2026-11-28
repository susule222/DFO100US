package protocol

import (
	"encoding/binary"
	"testing"
)

// The recovered 2.24 row is 95 fixed bytes plus the name (101 with a six-byte
// name). Two characters, one with a six-byte name and one with a five-byte
// name, must both parse to the same fixed size, so the per-row length has to
// be name-independent. Length alone cannot catch a field-order error: the
// earlier arrangement kept this total while shifting every field after the
// second u8-after-u32 pair by one byte (see the fatigue-offset test).
func TestCharacterListV224RowWidth(t *testing.T) {
	roles := []CharacterRow{
		{Slot: 0, Name: "us100a", Profession: 0, Advancement: 0, Level: 1},
		{Slot: 1, Name: "us100b", Profession: 3, Advancement: 0, Level: 1},
	}
	p, err := CharacterListV224(8, roles)
	if err != nil {
		t.Fatal(err)
	}
	// Header: 3 + 2 + 2 + 2 + 4 + 2 = 15 bytes. Tail: 1 + 1 + 4 + 4 = 10.
	// Each row: 95 fixed bytes plus the name. Both names here are six bytes.
	want := 15 + (95+6) + (95+6) + 10
	if len(p) != want {
		t.Fatalf("roster size=%d want=%d", len(p), want)
	}
	if p[0] != 2 || p[1] != 0 || p[2] != 0 {
		t.Fatal("mode header")
	}
}

func TestCharacterListV224PacksProfessionAndAdvancement(t *testing.T) {
	p, err := CharacterListV224(2, []CharacterRow{
		{Slot: 0, Name: "a", Profession: 3, Advancement: 2, Level: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Header is 15 bytes. Row: u16 slot (2) + u32 len (4) + name (1) = 7; the
	// six bytes follow at +7: {0, packed, 0, level, 0, 0}. Packed = 3|2<<4.
	// The packed byte is the second one: the bisect row whose second byte held
	// 3 in the low nibble rendered the Mage portrait, while the first and the
	// third byte had no such effect.
	if p[15+7+0] != 0 {
		t.Fatalf("byte 1 = %#x want 0", p[15+7+0])
	}
	packed := p[15+7+1]
	if packed != 0x23 {
		t.Fatalf("packed profession/advancement byte=%#x want 0x23", packed)
	}
	if p[15+7+2] != 0 {
		t.Fatalf("byte 3 = %#x want 0", p[15+7+2])
	}
	if p[15+7+3] != 5 {
		t.Fatalf("level byte=%d want 5", p[15+7+3])
	}
}

// The cursor trace pins the fatigue pair at packet offsets 108 and 110 for a
// six-byte name (row-relative 93 and 95). A one-byte field shift here
// previously made the client read the fatigue byte 0x9C inside a neighbouring
// u32 and render 0x9C000000 as -1677721600.
func TestCharacterListV224FatigueOffsets(t *testing.T) {
	p, err := CharacterListV224(2, []CharacterRow{
		{Slot: 0, Name: "us100a", Level: 1, FatigueRemaining: 156, FatigueBonus: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := 15
	if got := binary.LittleEndian.Uint16(p[row+93:]); got != 156 {
		t.Fatalf("fatigue remaining at 93 = %d want 156", got)
	}
	if got := binary.LittleEndian.Uint16(p[row+95:]); got != 7 {
		t.Fatalf("fatigue bonus at 95 = %d want 7", got)
	}
	if got := binary.LittleEndian.Uint32(p[row+89:]); got != 0 {
		t.Fatalf("u32 before fatigue = %#x want 0", got)
	}
}

func TestCharacterListV224EncodesAppearanceRows(t *testing.T) {
	p, err := CharacterListV224(2, []CharacterRow{
		{
			Slot:             0,
			Name:             "us100a",
			Profession:       0,
			Level:            16,
			FatigueRemaining: 254,
			FatigueBonus:     10,
			Equipment: []Equipment{
				{Slot: 0, Item: 39165},
				{Slot: 8, Item: 42215},
				{Slot: 12, Item: 100001},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLen := 15 + (95 + 6 + 3*31) + 10
	if len(p) != wantLen {
		t.Fatalf("roster size=%d want=%d", len(p), wantLen)
	}
	row := 15
	// After u16 slot (2) + u32 nameLen (4) + 6-byte name + 6 u8 + 2 u32 = +26:
	eqCountOff := row + 26
	if p[eqCountOff] != 3 {
		t.Fatalf("appearance count = %d, want 3", p[eqCountOff])
	}
	wantSlots := [][2]uint32{{0, 39165}, {8, 42215}, {12, 100001}}
	for i, w := range wantSlots {
		entry := p[eqCountOff+1+i*31 : eqCountOff+1+(i+1)*31]
		if uint32(entry[0]) != w[0] || binary.LittleEndian.Uint32(entry[1:5]) != w[1] {
			t.Fatalf("slot %d = (%d,%d), want (%d,%d)", i, entry[0], binary.LittleEndian.Uint32(entry[1:5]), w[0], w[1])
		}
	}
	// Fatigue pair shifts cleanly by 3*31 bytes after the appearance block.
	if got := binary.LittleEndian.Uint16(p[row+93+3*31:]); got != 254 {
		t.Fatalf("fatigue remaining = %d, want 254", got)
	}
	if got := binary.LittleEndian.Uint16(p[row+95+3*31:]); got != 10 {
		t.Fatalf("fatigue bonus = %d, want 10", got)
	}
	if _, err := CharacterListV224(2, []CharacterRow{
		{Slot: 0, Name: "a", Level: 1, Equipment: []Equipment{{Slot: 0, Item: 1}, {Slot: 0, Item: 2}}},
	}); err == nil {
		t.Fatal("accepted duplicate appearance slot")
	}
}

func TestCharacterListV224ShowAuraFlag(t *testing.T) {
	p, err := CharacterListV224(2, []CharacterRow{
		{Slot: 0, Name: "us100a", Level: 100},
		{Slot: 1, Name: "us100b", Level: 100, HideAura: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Row 0 starts at byte 15, row width for 6-byte name with 0 appearance rows is 101 bytes.
	// ShowAura u16 (0x143807cc2) is at row-relative offset 84 (right after u32@80, before u8x3@86).
	row0 := 15
	row1 := 15 + 101
	if got := binary.LittleEndian.Uint16(p[row0+84:]); got != 1 {
		t.Fatalf("default roster row showAura = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(p[row1+84:]); got != 0 {
		t.Fatalf("HideAura roster row showAura = %d, want 0", got)
	}
}
