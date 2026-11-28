package protocol

import (
	"encoding/binary"
	"fmt"
)

// Verified US100 Skin Storage (皮肤仓库) opcodes recovered from DFO_unpacked.exe (v2.24.0.124):
//   - NOTI 1523 (0x5f3, 0x1434f3010): broadcast animated Instant Emoticon above an actor's head
//   - NOTI 1545 (0x609, 0x1428486f0 -> 0x14284a4a0): owned skin storage list per category
//   - NOTI 1546 (0x60a, 0x1428485b0 -> 0x142849770): currently equipped skin storage slots
//   - NOTI 1547 (0x60b, 0x1428484e0): recently acquired skins list (Summary tab)
//   - CMD  1551 (0x60f, 0x142380e79 & 0x14284aaee): trigger Instant Emoticon (quick-slot or chat keyword)
//   - CMD  1564 (0x61c, 0x14247ea21 & 0x14248b25f) / ACK 1564 (0x1434c2100): Linus Weapon Mold extraction
//   - CMD  1565 (0x61d, 0x14284adf5) / ACK 1565 (0x142845c40 -> 0x142849240): equip/unequip skin
//   - CMD  2038 (0x7f6, 0x14284ab50) / ACK 2038 (0x142845c10): save Emoticon chat keyword triggers
//   - NOTI 2250 (0x8ca, 0x142848400): restore/update Emoticon chat keyword triggers
const (
	NotiInstantEmoticon    uint16 = 1523 // 0x5f3
	NotiSkinStorageList    uint16 = 1545 // 0x609
	NotiSkinStorageEquip   uint16 = 1546 // 0x60a
	NotiSkinStorageRecent  uint16 = 1547 // 0x60b
	CmdInstantEmoticon     uint16 = 1551 // 0x60f
	CmdWeaponSkinMold      uint16 = 1564 // 0x61c
	CmdSkinStorageEquip    uint16 = 1565 // 0x61d
	CmdEmoticonKeywordSave uint16 = 2038 // 0x7f6
	NotiEmoticonKeywords   uint16 = 2250 // 0x8ca

	SkinCatDamageFont        byte = 0 // Normal + Cumulative Damage Font (2 x u32 in NOTI 1546)
	SkinCatSkillCutscene     byte = 1 // Awakening Skill Cut-Scene (u16 count + N x u32 in NOTI 1546)
	SkinCatPartyFrame        byte = 2 // Party Frame (1 x u32 in NOTI 1546; owned[2] also holds PartyRequestFrame)
	SkinCatEmoticon          byte = 3 // Instant Emoticon (4 x u32 quick-slots in NOTI 1546 & ACK 1565)
	SkinCatWeapon            byte = 4 // Weapon Skin (4-byte char entries in NOTI 1545; 1 x u32 in NOTI 1546)
	SkinCatPartyRequestFrame byte = 6 // Party Request Frame (1 x u32 in NOTI 1546; client looks up owned in cat 2)

	// Built-in default skin IDs from list/skin.lst and DFO_unpacked.exe (0x14284ac50 / 0x142849d2f).
	DefaultDamageFontNormal  uint32 = 1
	DefaultDamageFontSum     uint32 = 99999999
	DefaultSkillCutscene     uint32 = 30000
	DefaultPartyFrame        uint32 = 20000
	DefaultPartyRequestFrame uint32 = 50000
	UnsetPartyRequestFrame   uint32 = 99999999

	EmoticonSlotCount      = 4
	EmoticonKeywordRowSize = 25 // 0x19: u32 emoticonID + [21]byte NUL-terminated keyword
)

// SkinRecentEntry is one entry in NOTI 1547 (0x60b, Recently Acquired skins).
type SkinRecentEntry struct {
	Category byte   `json:"c"`
	SkinID   uint32 `json:"i"`
}

// EmoticonKeywordEntry is one 25-byte (0x19) entry in CMD 2038 / NOTI 2250.
type EmoticonKeywordEntry struct {
	EmoticonID uint32   `json:"id"`
	Keyword    [21]byte `json:"kw"`
}

// WeaponSkinMoldRequest is the decoded 9-byte payload of CMD 1564 (0x61c).
type WeaponSkinMoldRequest struct {
	WeaponSlot   uint32
	MoldMode     byte
	MaterialSlot uint32
}

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
	case SkinCatEmoticon:
		// 0x142849ae8: fixed 4 x u32 emoticon quick-slots (0 = empty slot).
		for i := 0; i < EmoticonSlotCount; i++ {
			p = add32(p, slotAt(i))
		}
	default:
		// Categories 2, 4, 6: single u32 active skin ID.
		p = add32(p, slotAt(0))
	}
	return p
}

// SkinStorageRecentlyAcquired encodes NOTI 1547 (0x60b, 0x1428484e0):
// u8 count + count * (u8 category, u32 skinID), chronological (Summary tab iterates backwards).
func SkinStorageRecentlyAcquired(entries []SkinRecentEntry) []byte {
	if len(entries) > 255 {
		entries = entries[len(entries)-255:]
	}
	p := make([]byte, 0, 1+len(entries)*5)
	p = append(p, byte(len(entries)))
	for _, e := range entries {
		p = append(p, e.Category)
		p = add32(p, e.SkinID)
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

// DecodeInstantEmoticonCmd decodes CMD 1551 (0x60f, u32 emoticonSkinID).
func DecodeInstantEmoticonCmd(p []byte) (uint32, error) {
	if len(p) < 4 {
		return 0, fmt.Errorf("instant emoticon cmd requires 4 bytes, got %d", len(p))
	}
	return binary.LittleEndian.Uint32(p[0:4]), nil
}

// InstantEmoticonBroadcast encodes NOTI 1523 (0x5f3, 0x1434f3010):
// u16 actorServerID + u32 emoticonSkinID (6 bytes).
func InstantEmoticonBroadcast(actorServerID uint16, emoticonID uint32) []byte {
	p := make([]byte, 0, 6)
	p = add16(p, actorServerID)
	p = add32(p, emoticonID)
	return p
}

// DecodeEmoticonKeywordSaveCmd decodes CMD 2038 (0x7f6, 0x14284ab50):
// u8 count + count * 25 bytes (u32 emoticonID + [21]byte keyword).
func DecodeEmoticonKeywordSaveCmd(p []byte) ([]EmoticonKeywordEntry, error) {
	if len(p) < 1 {
		return nil, fmt.Errorf("emoticon keyword save requires at least 1 byte")
	}
	count := int(p[0])
	need := 1 + count*EmoticonKeywordRowSize
	if len(p) < need {
		return nil, fmt.Errorf("emoticon keyword save truncated: need %d bytes for %d entries, got %d", need, count, len(p))
	}
	out := make([]EmoticonKeywordEntry, 0, count)
	off := 1
	for i := 0; i < count; i++ {
		var e EmoticonKeywordEntry
		e.EmoticonID = binary.LittleEndian.Uint32(p[off : off+4])
		copy(e.Keyword[:], p[off+4:off+EmoticonKeywordRowSize])
		out = append(out, e)
		off += EmoticonKeywordRowSize
	}
	return out, nil
}

// EmoticonKeywordSaveSuccess encodes ACK 2038 (0x7f6, 0x142845c10): u8 1.
func EmoticonKeywordSaveSuccess() []byte {
	return []byte{1}
}

// EmoticonKeywordListNoti encodes NOTI 2250 (0x8ca, 0x142848400):
// u8 count + count * 25 bytes.
func EmoticonKeywordListNoti(entries []EmoticonKeywordEntry) []byte {
	if len(entries) > 255 {
		entries = entries[:255]
	}
	p := make([]byte, 0, 1+len(entries)*EmoticonKeywordRowSize)
	p = append(p, byte(len(entries)))
	for _, e := range entries {
		p = add32(p, e.EmoticonID)
		p = append(p, e.Keyword[:]...)
	}
	return p
}

// DecodeWeaponSkinMoldCmd decodes CMD 1564 (0x61c, 0x14247ea21 & 0x14248b25f):
// u32 weaponSlot + u8 moldMode + u32 materialSlot (9 bytes + Blowfish pad).
func DecodeWeaponSkinMoldCmd(p []byte) (WeaponSkinMoldRequest, error) {
	var r WeaponSkinMoldRequest
	if len(p) < 9 {
		return r, fmt.Errorf("weapon skin mold requires 9 bytes, got %d", len(p))
	}
	r.WeaponSlot = binary.LittleEndian.Uint32(p[0:4])
	r.MoldMode = p[4]
	r.MaterialSlot = binary.LittleEndian.Uint32(p[5:9])
	return r, nil
}

// WeaponSkinMoldSuccess encodes ACK 1564 (0x61c, 0x1434c2100): u8 1.
func WeaponSkinMoldSuccess() []byte {
	return []byte{1}
}

// WeaponSkinMoldRefused encodes ACK 1564 error (0x1434c2162): u8 0 + u16 errorCode.
func WeaponSkinMoldRefused(code uint16) []byte {
	return add16([]byte{0}, code)
}
