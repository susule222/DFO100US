package protocol

import (
	"encoding/binary"
	"testing"
)

func TestDecodeUpgradeItemAndSuccess(t *testing.T) {
	// Build a CMD 80 amplify ticket request: mode=1, space=3 (worn), slot=14,
	// tpl=400090164, ticket_slot=75, protect_slot=0xffff, name="Taiji", extras=0xffff,0xffff,0
	var raw []byte
	raw = append(raw, 1, 3)
	raw = add16(raw, 14)
	raw = add32(raw, 400090164)
	raw = add16(raw, 75)
	raw = add16(raw, 0xffff)
	raw = add32(raw, 5)
	raw = append(raw, []byte("Taiji")...)
	raw = add16(raw, 0xffff)
	raw = add16(raw, 0xffff)
	raw = append(raw, 0)
	// Pad to 8-byte cipher block
	for len(raw)%8 != 0 {
		raw = append(raw, 0)
	}

	req, err := DecodeUpgradeItem(raw)
	if err != nil {
		t.Fatalf("DecodeUpgradeItem failed: %v", err)
	}
	if req.Mode != 1 || req.TargetSpace != 3 || req.TargetSlot != 14 || req.TargetTemplate != 400090164 || req.TicketSlot != 75 || req.ProtectSlot != 0xffff || req.ItemName != "Taiji" {
		t.Fatalf("unexpected decoded request: %+v", req)
	}

	ack := UpgradeItemSuccess(UpgradeItemResult{
		Mode:            1,
		TicketSlot:      75,
		TicketRemaining: 0,
		ProtectSlot:     0xffff,
		UseTicketType:   1,
		OldLevel:        0,
		Outcome:         0,
		NewLevel:        10,
		TargetSpace:     3,
		TargetSlot:      14,
		AuxSlot:         0xffff,
		AuxRemaining:    0,
	})
	// Expected exact length for outcome != 3:
	// ok(1) + mode(1) + ticketSlot(2) + ticketRem(4) + protectSlot(2) +
	// useTicketType(1) + oldLv(1) + outcome(1) + newLv(1) + targetSpace(1) + targetSlot(2) +
	// auxSlot(2) + auxRem(4) + extra2(2) + extra10(10) = 35 bytes
	if len(ack) != 35 {
		t.Fatalf("expected 35-byte ACK 80, got %d", len(ack))
	}
	if ack[0] != 1 || ack[1] != 1 || binary.LittleEndian.Uint16(ack[2:4]) != 75 || ack[10] != 1 || ack[12] != 0 || ack[13] != 10 || ack[14] != 3 || binary.LittleEndian.Uint16(ack[15:17]) != 14 || binary.LittleEndian.Uint16(ack[17:19]) != 0xffff {
		t.Fatalf("unexpected ACK 80 bytes: %x", ack)
	}
}

func TestDecodePurifyAndInvestAmplify(t *testing.T) {
	// CMD 204
	p204 := add32(add16(add32(add16(nil, 9), 100331477), 80), 1183)
	p204 = append(p204, 0, 0, 0, 0)
	r204, err := DecodePurifyItem(p204)
	if err != nil {
		t.Fatalf("DecodePurifyItem failed: %v", err)
	}
	if r204.TargetSlot != 9 || r204.TargetTemplate != 100331477 || r204.MaterialSlot != 80 || r204.MaterialTemplate != 1183 {
		t.Fatalf("unexpected CMD 204: %+v", r204)
	}
	ack204 := PurifyItemSuccess(80, 0, 9, 3, 18)
	if len(ack204) != 12 || ack204[0] != 1 || ack204[9] != 3 || binary.LittleEndian.Uint16(ack204[10:12]) != 18 {
		t.Fatalf("unexpected ACK 204: %x", ack204)
	}

	// CMD 205 mode=2 (Golden Grimoire)
	p205 := append([]byte{2}, byte(9), 0)
	p205 = add32(p205, 100331477)
	p205 = add16(p205, 81)
	p205 = add32(p205, 10000902)
	p205 = append(p205, 3) // Strength
	p205 = add32(p205, 4)
	p205 = append(p205, []byte("Gear")...)
	for len(p205)%8 != 0 {
		p205 = append(p205, 0)
	}
	r205, err := DecodeInvestAmplifyOption(p205)
	if err != nil {
		t.Fatalf("DecodeInvestAmplifyOption failed: %v", err)
	}
	if r205.Mode != 2 || r205.TargetSlot != 9 || r205.MaterialSlot != 81 || r205.ChosenStat != 3 || r205.ItemName != "Gear" {
		t.Fatalf("unexpected CMD 205: %+v", r205)
	}
	ack205 := InvestAmplifyOptionSuccess(2, 81, 0, 9, 3, 45, 5)
	if len(ack205) != 14 || ack205[0] != 1 || ack205[1] != 2 || ack205[10] != 3 || binary.LittleEndian.Uint16(ack205[11:13]) != 45 || ack205[13] != 5 {
		t.Fatalf("unexpected ACK 205: %x", ack205)
	}
}
