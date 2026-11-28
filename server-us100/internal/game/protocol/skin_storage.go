package protocol

import (
	"encoding/binary"
	"fmt"
)

// Verified US100 Skin Storage (皮肤仓库) opcodes recovered from DFO_unpacked.exe:
//   - NOTI 1545 (0x609, 0x1428486f0 -> 0x14284a4a0): owned skin storage list per category
//   - NOTI 1546 (0x60a, 0x1428485b0 -> 0x142849770): currently equipped skin storage slots
//   - CMD  1565 (0x61d, 0x14284adf5) / ACK 1565 (0x142845c40 -> 0x142849240): equip/unequip skin
const (
	NotiSkinStorageList  uint16 = 1545 // 0x609
	NotiSkinStorageEquip uint16 = 1546 // 0x60a
	CmdSkinStorageEquip  uint16 = 1565 // 0x61d

	SkinCatDamageFont        byte = 0 // Normal + Cumulative Damage Font (2 x u32 in NOTI 1546)
	SkinCatSkillCutscene     byte = 1 // Awakening Skill Cut-Scene (u16 count + N x u32 in NOTI 1546)
	SkinCatPartyFrame        byte = 2 // Party Frame (1 x u32 in NOTI 1546)
	SkinCatEmoticon          byte = 3 // Instant Emoticon (1 x u32 in NOTI 1546)
	SkinCatWeapon            byte = 4 // Weapon Skin (4-byte char entries in NOTI 1545; 1 x u32 in NOTI 1546)
	SkinCatPartyRequestFrame byte = 6 // Party Request Frame (1 x u32 in NOTI 1546)
)

// SkinStorageOwnedList encodes NOTI 1545 (0x609) for one Skin Storage category (0..6).
// Expire timestamp 0 marks permanent ownership (verified at 0x142847df0).
func SkinStorageOwnedList(category byte, charSkins, acctSkins []uint32) []byte {
	p := make([]byte, 0, 1+2+len(charSkins)*8+2+len(acctSkins)*8)
	p = append(p, category)
	p = add16(p, uint16(len(charSkins)))
	for _, id := range charSkins {
		p = add32(p, id)
		if category != SkinCatWeapon {
			p = add32(p, 0) // permanent (no expiry)
		}
	}
	p = add16(p, uint16(len(acctSkins)))
	for _, id := range acctSkins {
		p = add32(p, id)
		p = add32(p, 0)
	}
	return p
}

// SkinStorageEquipped encodes NOTI 1546 (0x60a) for one Skin Storage category (0..6).
func SkinStorageEquipped(category byte, slots []uint32) []byte {
	p := make([]byte, 0, 1+2+len(slots)*4)
	p = append(p, category)
	slotAt := func(i int) uint32 {
		if i < len(slots) {
			return slots[i]
		}
		return 0
	}
	switch category {
	case SkinCatDamageFont:
		// 0x14284985a: normal damage font u32, then cumulative damage font u32.
		p = add32(p, slotAt(0))
		p = add32(p, slotAt(1))
	case SkinCatSkillCutscene:
		// 0x142849918: u16 count + count * u32.
		var active []uint32
		for _, id := range slots {
			if id != 0 {
				active = append(active, id)
			}
		}
		p = add16(p, uint16(len(active)))
		for _, id := range active {
			p = add32(p, id)
		}
	default:
		// Categories 2, 3, 4, 6: single u32 active skin ID.
		p = add32(p, slotAt(0))
	}
	return p
}

// DecodeSkinStorageEquipCmd decodes CMD 1565 (0x61d, 44 bytes = u32 category + 10*u32 slots).
func DecodeSkinStorageEquipCmd(p []byte) (uint32, [10]uint32, error) {
	var slots [10]uint32
	if len(p) < 44 {
		return 0, slots, fmt.Errorf("skin storage equip requires 44 bytes, got %d", len(p))
	}
	cat := binary.LittleEndian.Uint32(p[0:4])
	for i := 0; i < 10; i++ {
		slots[i] = binary.LittleEndian.Uint32(p[4+i*4 : 8+i*4])
	}
	return cat, slots, nil
}

// SkinStorageEquipSuccess encodes ACK 1565 (0x61d, 45 bytes = u8 1 + 44-byte state echo).
func SkinStorageEquipSuccess(category uint32, slots [10]uint32) []byte {
	p := make([]byte, 0, 45)
	p = append(p, 1)
	p = add32(p, category)
	for _, id := range slots {
		p = add32(p, id)
	}
	return p
}
