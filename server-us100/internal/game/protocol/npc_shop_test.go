package protocol

import (
	"encoding/binary"
	"testing"
)

func TestDecodeNPCShopBuyRequest(t *testing.T) {
	raw := make([]byte, 24)
	binary.LittleEndian.PutUint32(raw[0:4], 50052389)
	binary.LittleEndian.PutUint32(raw[4:8], 2)
	binary.LittleEndian.PutUint32(raw[8:12], 100000399)
	binary.LittleEndian.PutUint32(raw[12:16], 100000827)
	raw[16] = 0
	req, err := DecodeNPCShopBuyRequest(raw)
	if err != nil {
		t.Fatalf("DecodeNPCShopBuyRequest: %v", err)
	}
	if req.ItemTemplate != 50052389 || req.Quantity != 2 || req.ShopID != 100000399 || req.NPCID != 100000827 || req.TabIndex != 0 {
		t.Fatalf("unexpected decoded request: %+v", req)
	}
}

func TestNPCShopBuySuccessLayout(t *testing.T) {
	var row [CurrentItemRecordSize]byte
	binary.LittleEndian.PutUint16(row[0:2], 55)
	binary.LittleEndian.PutUint32(row[2:6], 50052389)
	binary.LittleEndian.PutUint32(row[6:10], 1)

	p, err := NPCShopBuySuccess(123456, 0, 0, 50, row, []NPCShopMaterialUpdate{
		{Template: 50052385, Remaining: 200},
	})
	if err != nil {
		t.Fatalf("NPCShopBuySuccess: %v", err)
	}
	wantLen := 1 + 16 + 0xb1 + 1 + 8
	if len(p) != wantLen {
		t.Fatalf("len=%d, want %d", len(p), wantLen)
	}
	if p[0] != 1 {
		t.Fatalf("ok=%d, want 1", p[0])
	}
	if got := binary.LittleEndian.Uint32(p[1:5]); got != 123456 {
		t.Fatalf("gold=%d, want 123456", got)
	}
	if got := binary.LittleEndian.Uint32(p[13:17]); got != 50 {
		t.Fatalf("point=%d, want 50", got)
	}
	if got := binary.LittleEndian.Uint16(p[17:19]); got != 55 {
		t.Fatalf("row slot=%d, want 55", got)
	}
	matOff := 1 + 16 + 0xb1
	if p[matOff] != 1 {
		t.Fatalf("mat count=%d, want 1", p[matOff])
	}
	if got := binary.LittleEndian.Uint32(p[matOff+1 : matOff+5]); got != 50052385 {
		t.Fatalf("mat template=%d, want 50052385", got)
	}
	if got := binary.LittleEndian.Uint32(p[matOff+5 : matOff+9]); got != 200 {
		t.Fatalf("mat remaining=%d, want 200", got)
	}
}

func TestNPCShopLimitsRoundTrip(t *testing.T) {
	// Live capture from Seria's room CoinShop (NPC 20673 = 0x50c1)
	npc, err := DecodeNPCShopLimitsRequest([]byte{0xc1, 0x50, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if err != nil || npc != 20673 {
		t.Fatalf("DecodeNPCShopLimitsRequest: npc=%d err=%v", npc, err)
	}
	pkt := NPCShopLimitsSuccess(
		nil,
		[]NPCShopLimitCount{{Template: 50052392, Count: 1}},
		nil,
		[]NPCShopLimitCount{{Template: 50052389, Count: 1}},
	)
	if len(pkt) != 1+4*4+2*8 {
		t.Fatalf("unexpected ACK 707 len=%d", len(pkt))
	}
	if pkt[0] != 1 {
		t.Fatalf("ok=%d, want 1", pkt[0])
	}
	// List 1 (charAccum) = 0, List 2 (acctAccum) = 1
	if binary.LittleEndian.Uint32(pkt[1:5]) != 0 || binary.LittleEndian.Uint32(pkt[5:9]) != 1 {
		t.Fatalf("unexpected list headers")
	}
	if binary.LittleEndian.Uint32(pkt[9:13]) != 50052392 || binary.LittleEndian.Uint32(pkt[13:17]) != 1 {
		t.Fatalf("unexpected acctAccum entry")
	}
}
