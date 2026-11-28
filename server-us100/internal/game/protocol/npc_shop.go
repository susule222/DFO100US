package protocol

import (
	"encoding/binary"
	"fmt"
)

// Verified US100 NPC shop opcodes from DFO_unpacked.exe:
//   - CMD 21  (0x15, sender 0x144909d30..0x14490a235) / ACK 21  (handler 0x14349cc90): buy item from NPC shop
//   - CMD 22  (0x16, sender 0x144917ae0..0x144917bb4) / ACK 22  (handler 0x1434c22c0): sell item to NPC shop
//   - CMD 309 (0x135, sender 0x14315dedf..0x14315def4): query NPC shop buyback list
//   - CMD 707 (0x2c3, sender 0x14314eee0..0x14314ef1a) / ACK 707 (handler 0x1434c4590): query NPC shop purchase limits
const (
	CmdNPCShopBuy         uint16 = 21
	CmdNPCShopSell        uint16 = 22
	CmdNPCShopBuybackList uint16 = 309
	CmdNPCShopLimits      uint16 = 707
)

// NPCShopBuyRequest is the decoded CMD 21 (0x15) payload sent at 0x14490a1c8..0x14490a235:
//   [0..3]   u32 ItemTemplate
//   [4..7]   u32 Quantity
//   [8..11]  u32 ShopID
//   [12..15] u32 NPCID
//   [16]     u8  TabIndex
type NPCShopBuyRequest struct {
	ItemTemplate uint32
	Quantity     uint32
	ShopID       uint32
	NPCID        uint32
	TabIndex     byte
}

func DecodeNPCShopBuyRequest(p []byte) (NPCShopBuyRequest, error) {
	var r NPCShopBuyRequest
	if len(p) < 17 {
		return r, fmt.Errorf("npc shop buy requires at least 17 bytes, got %d", len(p))
	}
	r.ItemTemplate = binary.LittleEndian.Uint32(p[0:4])
	r.Quantity = binary.LittleEndian.Uint32(p[4:8])
	r.ShopID = binary.LittleEndian.Uint32(p[8:12])
	r.NPCID = binary.LittleEndian.Uint32(p[12:16])
	r.TabIndex = p[16]
	return r, nil
}

// NPCShopMaterialUpdate updates the client's remaining stack count of one
// material item consumed during an NPC shop purchase (0x14349ce86..0x14349d0f8).
// Remaining == 0 removes the item from its inventory space.
type NPCShopMaterialUpdate struct {
	Template  uint32
	Remaining uint32
}

// NPCShopBuySuccess encodes ACK 21 (0x15, handler 0x14349cc90):
//   - u8  ok = 1
//   - u32 gold (primary bag gold, [0x143b1eb80]+0x98)
//   - u32 gold2 (secondary currency, [0x143b1eb60]+0x98)
//   - u32 reserved (0)
//   - u32 point (shop point / mileage counter at 0x14349cf56)
//   - 0xb1 bytes: US224 inventory row of the bought item
//   - u8  len(materials)
//   - len(materials) * (u32 template, u32 remaining)
func NPCShopBuySuccess(gold, gold2, reserved, point uint32, row [CurrentItemRecordSize]byte, materials []NPCShopMaterialUpdate) ([]byte, error) {
	const rowSize = 0xb1
	if len(materials) > 255 {
		return nil, fmt.Errorf("too many material updates: %d", len(materials))
	}
	for _, tail := range row[rowSize:] {
		if tail != 0 {
			return nil, fmt.Errorf("unsupported US224 item tail in NPCShopBuySuccess")
		}
	}
	p := make([]byte, 0, 1+16+rowSize+1+len(materials)*8)
	p = append(p, 1)
	p = add32(p, gold)
	p = add32(p, gold2)
	p = add32(p, reserved)
	p = add32(p, point)
	p = append(p, row[:rowSize]...)
	p = append(p, byte(len(materials)))
	for _, m := range materials {
		p = add32(p, m.Template)
		p = add32(p, m.Remaining)
	}
	return p, nil
}

// NPCShopBuyRefused encodes a failed ACK 21 (0x15, handler 0x14349dd8b):
//   u8 ok=0, u16 code (4=insufficient funds/material, 10=bag full, 14=limit reached).
func NPCShopBuyRefused(code uint16) []byte {
	return add16([]byte{0}, code)
}

// DecodeNPCShopLimitsRequest decodes CMD 707 (0x2c3, sender 0x14314eee0): u32 npcID.
func DecodeNPCShopLimitsRequest(p []byte) (uint32, error) {
	if len(p) < 4 {
		return 0, fmt.Errorf("npc shop limits requires at least 4 bytes, got %d", len(p))
	}
	return binary.LittleEndian.Uint32(p[0:4]), nil
}

// NPCShopLimitCount is one (itemTemplate, boughtCount) pair in ACK 707.
type NPCShopLimitCount struct {
	Template uint32
	Count    uint32
}

// NPCShopLimitsSuccess encodes ACK 707 (0x2c3, handler 0x1434c4590):
//   u8 ok=1, followed by 4 count-prefixed lists of (u32 template, u32 boughtCount):
//   charAccum, acctAccum, charPeriodic, acctPeriodic.
func NPCShopLimitsSuccess(charAccum, acctAccum, charPeriodic, acctPeriodic []NPCShopLimitCount) []byte {
	lists := [][]NPCShopLimitCount{charAccum, acctAccum, charPeriodic, acctPeriodic}
	total := 1 + 4*4
	for _, l := range lists {
		total += len(l) * 8
	}
	p := make([]byte, 0, total)
	p = append(p, 1)
	for _, l := range lists {
		p = add32(p, uint32(len(l)))
		for _, item := range l {
			p = add32(p, item.Template)
			p = add32(p, item.Count)
		}
	}
	return p
}
