package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestUS224FatigueLengthAndFields(t *testing.T) {
	p, err := Fatigue(13, 273, 13)
	if err != nil {
		t.Fatal(err)
	}
	// US100 NOTI 36 (0x1434f7020..0x1434f721f): 5*u16 + 1*u8 = 11 bytes.
	if len(p) != 11 {
		t.Fatalf("expected 11-byte NOTI 36 payload, got %d", len(p))
	}
	if binary.LittleEndian.Uint16(p[0:]) != 13 ||
		binary.LittleEndian.Uint16(p[2:]) != 273 ||
		binary.LittleEndian.Uint16(p[4:]) != 13 ||
		binary.LittleEndian.Uint16(p[6:]) != 0 ||
		binary.LittleEndian.Uint16(p[8:]) != 0 ||
		p[10] != 0 {
		t.Fatalf("unexpected NOTI 36 fields: %x", p)
	}
}

func TestUS224ClearRewardEmptyAndNeoExtraCard(t *testing.T) {
	empty, err := ClearRewardUS224(ClearRewardUS224State{
		BaseExperience:    1000,
		ScoreExperience:   200,
		NeoExperience:     120,
		GrowthExperience:  240,
		MonsterExperience: 555,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 233 {
		t.Fatalf("expected 233-byte empty US100 NOTI 35, got %d", len(empty))
	}
	// Native 0x1434d3f85 subtracts offset 8 from offset 0 for DisplayedBase.
	if got := binary.LittleEndian.Uint32(empty[0:]); got != 1120 {
		t.Fatalf("wire base=%d, want 1120", got)
	}
	if got := binary.LittleEndian.Uint32(empty[4:]); got != 200 {
		t.Fatalf("score=%d, want 200", got)
	}
	if got := binary.LittleEndian.Uint32(empty[8:]); got != 120 {
		t.Fatalf("neo exp=%d, want 120", got)
	}
	if got := binary.LittleEndian.Uint32(empty[33:]); got != 240 {
		t.Fatalf("growth exp=%d, want 240", got)
	}
	// Empty layout: 131 + 8 (G0) + 4 (cost) + 8 (G1) + 8 (G2) = 159.
	// Middle 26B starts at 159: +8 is resultWindowFlag (167), +10 is MonsterExp (169).
	if empty[167] != 1 {
		t.Fatalf("result window flag=%d, want 1", empty[167])
	}
	if got := binary.LittleEndian.Uint32(empty[169:]); got != 555 {
		t.Fatalf("monster exp=%d, want 555", got)
	}

	// Non-empty Group 0 (1 row = +8B) and Group 2 Neo extra card (1 row + 1B style = +9B).
	s := ClearRewardUS224State{
		BaseExperience:    1000,
		ScoreExperience:   100,
		NeoExperience:     110,
		MonsterExperience: 777,
		NeoCardStyle:      1,
	}
	s.Cards[0] = []CardReward{{Template: 0, Amount: 420}}
	s.NeoCards[0] = []CardReward{{Template: 0, Amount: 430}}
	withCards, err := ClearRewardUS224(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(withCards) != 233+8+9 {
		t.Fatalf("expected 250-byte NOTI 35 with G0+G2 cards, got %d", len(withCards))
	}
	// Group 0 participant 0 at offset 131: count=1, template=0, amount=420.
	if withCards[131] != 1 || binary.LittleEndian.Uint32(withCards[132:]) != 0 || binary.LittleEndian.Uint32(withCards[136:]) != 420 {
		t.Fatalf("group 0 row mismatch: %x", withCards[131:140])
	}
	// Group 2 participant 0 starts at 131 + (8+8) + 4 + 8 = 159.
	if withCards[159] != 1 || binary.LittleEndian.Uint32(withCards[160:]) != 0 || binary.LittleEndian.Uint32(withCards[164:]) != 430 || withCards[168] != 1 {
		t.Fatalf("group 2 neo row mismatch: %x", withCards[159:169])
	}
	// MonsterExperience shifts by +8+9 = +17 -> 169+17 = 186.
	if got := binary.LittleEndian.Uint32(withCards[186:]); got != 777 {
		t.Fatalf("shifted monster exp=%d, want 777", got)
	}
}

func TestCardSelectedWithExtraNeoFlag(t *testing.T) {
	base, err := CardSelected(2)
	if err != nil {
		t.Fatal(err)
	}
	noExtra, err := CardSelectedWithExtra(2, false)
	if err != nil || !bytes.Equal(base, noExtra) {
		t.Fatalf("CardSelectedWithExtra(false) diverged: %v", err)
	}
	withExtra, err := CardSelectedWithExtra(2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(withExtra) != 33 {
		t.Fatalf("expected 33B CMD 71 snapshot, got %d", len(withExtra))
	}
	// Row 0 byte 3 (offset 1 + 3 = 4) is participant 0's extra-card reveal flag (0x144bb2ad0).
	if withExtra[4] != 1 {
		t.Fatalf("participant 0 neo extra reveal flag=%d, want 1", withExtra[4])
	}
	// Row 2 byte 0 (offset 1 + 2*4 = 9) is card slot 2 assigned to participant 0.
	if withExtra[9] != 0 {
		t.Fatalf("card slot 2 owner=%d, want 0", withExtra[9])
	}
}
