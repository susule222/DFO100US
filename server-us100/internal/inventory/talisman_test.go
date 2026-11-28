package inventory

import (
	"dfolan/internal/game/protocol"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestTalismanAutoMigrationAndWireEncoding(t *testing.T) {
	// Simulate Char 48's saved bag where talisman 100401044 (光芒圣痕：拔刀奥义)
	// was previously placed in ordinary Equipment slot 10 alongside an ordinary item.
	raw := json.RawMessage(`{
		"level": 100,
		"inventory": {
			"version": "ordinary-bag-v1",
			"gold": 5000,
			"items": [],
			"equipment": [
				{"slot": 9, "template": 100331477, "durability": 0},
				{"slot": 10, "template": 100401044, "durability": 35}
			]
		}
	}`)
	bag, err := ReadBag(raw)
	if err != nil {
		t.Fatalf("ReadBag failed: %v", err)
	}
	if len(bag.Equipment) != 1 || bag.Equipment[0].Template != 100331477 {
		t.Fatalf("expected only non-talisman 100331477 left in Equipment, got %+v", bag.Equipment)
	}
	if len(bag.Talismans) != 1 || bag.Talismans[0].Template != 100401044 || bag.Talismans[0].Slot != 0 {
		t.Fatalf("expected talisman 100401044 migrated to space 0x2b slot 0, got %+v", bag.Talismans)
	}
	if bag.Talismans[0].Durability != 0 {
		t.Fatalf("expected migrated talisman durability=0, got %d", bag.Talismans[0].Durability)
	}
	if bag.Talismans[0].RuneSockets != DefaultTalismanRuneSockets {
		t.Fatalf("expected default rune sockets %v, got %v", DefaultTalismanRuneSockets, bag.Talismans[0].RuneSockets)
	}

	// Space 0 ordinary rows must NOT include the migrated talisman.
	for _, row := range bag.Rows() {
		tpl := binary.LittleEndian.Uint32(row[2:6])
		if tpl == 100401044 {
			t.Fatalf("talisman 100401044 must not appear in ordinary space 0 rows")
		}
	}

	// Space 0x2b restore payload: u8(0x2b) + u16(1) + 0xb1 bytes (no capacity header, no avatar trailers).
	talRows := bag.TalismanRows()
	if len(talRows) != 1 {
		t.Fatalf("expected 1 talisman row, got %d", len(talRows))
	}
	if got := [3]byte{
		talRows[0][protocol.TalismanRuneSocketsOffset],
		talRows[0][protocol.TalismanRuneSocketsOffset+1],
		talRows[0][protocol.TalismanRuneSocketsOffset+2],
	}; got != DefaultTalismanRuneSockets {
		t.Fatalf("expected rune socket bytes %v at +0x67, got %v", DefaultTalismanRuneSockets, got)
	}
	restore, err := protocol.US224TalismanRestore(talRows)
	if err != nil {
		t.Fatalf("US224TalismanRestore failed: %v", err)
	}
	if len(restore) != 1+2+0xb1 {
		t.Fatalf("expected space 0x2b restore len %d, got %d", 1+2+0xb1, len(restore))
	}
	if restore[0] != protocol.TalismanSpace || binary.LittleEndian.Uint16(restore[1:3]) != 1 {
		t.Fatalf("unexpected header: space=0x%x count=%d", restore[0], binary.LittleEndian.Uint16(restore[1:3]))
	}
	if tpl := binary.LittleEndian.Uint32(restore[3+2 : 3+6]); tpl != 100401044 {
		t.Fatalf("expected template 100401044 in restore payload, got %d", tpl)
	}
}

func TestAddRawEquipmentRoutesTalismanAndRunesToSpace2B(t *testing.T) {
	b := Bag{Version: "ordinary-bag-v1"}
	b, slots, err := b.AddRawEquipment([2]uint16{9, 64}, 100401044, 35, 1)
	if err != nil {
		t.Fatalf("AddRawEquipment(talisman) failed: %v", err)
	}
	if len(slots) != 1 || slots[0] != 0 {
		t.Fatalf("expected talisman slot 0, got %v", slots)
	}
	if len(b.Equipment) != 0 {
		t.Fatalf("talisman must not be placed in ordinary Equipment: %+v", b.Equipment)
	}
	if len(b.Talismans) != 1 || b.Talismans[0].Template != 100401044 {
		t.Fatalf("expected talisman in b.Talismans, got %+v", b.Talismans)
	}

	b, runeSlots, err := b.AddRunes(10600191, 2)
	if err != nil {
		t.Fatalf("AddRunes failed: %v", err)
	}
	if len(runeSlots) != 2 || runeSlots[0] != RuneSlotStart || runeSlots[1] != RuneSlotStart+1 {
		t.Fatalf("expected rune slots [80 81], got %v", runeSlots)
	}
	if rows := b.TalismanRows(); len(rows) != 3 {
		t.Fatalf("expected 3 space 0x2b rows (1 talisman + 2 runes), got %d", len(rows))
	}

	// Worn talisman in slot 33 encodes in space 3 with NO 8-byte avatar trailer.
	wornRow := EquipmentRow(BagEquipment{Slot: WornTalismanSlotMin, Template: 100401044, RuneSockets: DefaultTalismanRuneSockets})
	wornPayload, err := protocol.US224WornRestore([][protocol.CurrentItemRecordSize]byte{wornRow})
	if err != nil {
		t.Fatalf("US224WornRestore(slot 33) failed: %v", err)
	}
	if len(wornPayload) != 1+2+0xb1 {
		t.Fatalf("expected worn talisman len %d (no avatar trailer), got %d", 1+2+0xb1, len(wornPayload))
	}
}
