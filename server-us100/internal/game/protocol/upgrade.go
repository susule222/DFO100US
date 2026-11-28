package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	// CmdUpgradeItem is US v2.24 ENUM_CMDPACKET_UPGRADE_ITEM (0x50).
	// Used by Reinforcement Tickets ([equipment reinforcement ticket]),
	// Amplification Tickets ([equipment amplify reinforcement ticket]),
	// and Kiri/Klonter NPC reinforce/amplify.
	CmdUpgradeItem uint16 = 80

	// CmdPurifyItem is US v2.24 ENUM_CMDPACKET_PURIFY_ITEM (0xcc).
	// Purifies Otherverse Energy on equipment to grant Dimensional stats.
	CmdPurifyItem uint16 = 204

	// CmdInvestAmplifyOption is US v2.24 ENUM_CMDPACKET_INVEST_ITEM_AMPLIFY_OPTION (0xcd).
	// Used by Golden/Silver Amplification Grimoires to grant Dimensional stats
	// and (for mode 2 Golden Grimoires) initial amplification level.
	CmdInvestAmplifyOption uint16 = 205
)

// UpgradeItemRequest is the US v2.24 CMD 80 request body.
// Verified from DFO_unpacked.exe sender 0x143b2faec..0x143b2fc90:
//   u8  mode (0=reinforce, 1=amplify)
//   u8  target_space (0=ordinary bag, 3=worn)
//   u16 target_slot
//   u32 target_template
//   u16 ticket_slot (0xffff = NPC Kiri/Klonter without ticket)
//   u16 protect_slot (0xffff = none)
//   u32 name_len + [name_len]byte item_name
//   u16 extra_slot1 (0xffff = none)
//   u16 extra_slot2 (0xffff = none)
//   u8  extra_flag
type UpgradeItemRequest struct {
	Mode           byte
	TargetSpace    byte
	TargetSlot     uint16
	TargetTemplate uint32
	TicketSlot     uint16
	ProtectSlot    uint16
	ItemName       string
	ExtraSlot1     uint16
	ExtraSlot2     uint16
	ExtraFlag      byte
}

func DecodeUpgradeItem(p []byte) (UpgradeItemRequest, error) {
	var r UpgradeItemRequest
	if len(p) < 16 {
		return r, fmt.Errorf("upgrade item request truncated (%d bytes)", len(p))
	}
	r.Mode = p[0]
	if r.Mode > 1 {
		return r, fmt.Errorf("unsupported upgrade mode %d", r.Mode)
	}
	r.TargetSpace = p[1]
	if r.TargetSpace != 0 && r.TargetSpace != 3 {
		return r, fmt.Errorf("unsupported upgrade target space %d", r.TargetSpace)
	}
	r.TargetSlot = binary.LittleEndian.Uint16(p[2:4])
	r.TargetTemplate = binary.LittleEndian.Uint32(p[4:8])
	r.TicketSlot = binary.LittleEndian.Uint16(p[8:10])
	r.ProtectSlot = binary.LittleEndian.Uint16(p[10:12])
	nameLen := int(binary.LittleEndian.Uint32(p[12:16]))
	if nameLen < 0 || nameLen > 512 || 16+nameLen+5 > len(p) {
		return r, fmt.Errorf("invalid upgrade item name length %d in %d-byte payload", nameLen, len(p))
	}
	off := 16
	r.ItemName = string(p[off : off+nameLen])
	off += nameLen
	r.ExtraSlot1 = binary.LittleEndian.Uint16(p[off : off+2])
	r.ExtraSlot2 = binary.LittleEndian.Uint16(p[off+2 : off+4])
	r.ExtraFlag = p[off+4]
	off += 5
	for _, b := range p[off:] {
		if b != 0 {
			return r, fmt.Errorf("non-zero upgrade item padding")
		}
	}
	if r.TargetTemplate == 0 {
		return r, fmt.Errorf("upgrade item missing target template")
	}
	return r, nil
}

// UpgradeItemResult describes the ACK 80 success payload.
// Verified from DFO_unpacked.exe ACK 80 handler 0x1434c8ae4..0x1434c99a0:
//   u8  ok = 1
//   u8  mode (0=reinforce, 1=amplify)
//   u16 ticket_slot (0xffff if none)
//   u32 ticket_remaining
//   u16 protect_slot (0xffff if none)
//   u8  use_ticket_type (0=NPC +1, 1=fixed/increase ticket, 2=random-range ticket)
//   u8  old_level
//   u8  outcome (0=success, 1=fail keep level, 2=fail downgrade, 3=fail destroy)
//   u8  new_level
//   u8  target_space (0=bag, 3=worn)
//   u16 target_slot
//   [if outcome == 3: u8 return_count + return_count*(u16,u32,u32,u32)]
//   u16 aux_slot (0xffff if none)
//   u32 aux_remaining
//   u8  extra_u8_1
//   u8  extra_u8_2
//   [10]byte extra_10
type UpgradeItemResult struct {
	Mode            byte
	TicketSlot      uint16
	TicketRemaining uint32
	ProtectSlot     uint16
	UseTicketType   byte
	OldLevel        byte
	Outcome         byte
	NewLevel        byte
	TargetSpace     byte
	TargetSlot      uint16
	AuxSlot         uint16
	AuxRemaining    uint32
}

func UpgradeItemSuccess(res UpgradeItemResult) []byte {
	p := make([]byte, 0, 35)
	p = append(p, 1, res.Mode)
	p = add16(p, res.TicketSlot)
	p = add32(p, res.TicketRemaining)
	p = add16(p, res.ProtectSlot)
	p = append(p, res.UseTicketType, res.OldLevel, res.Outcome, res.NewLevel, res.TargetSpace)
	p = add16(p, res.TargetSlot)
	if res.Outcome == 3 {
		p = append(p, 0) // 0 broken return materials
	}
	auxSlot := res.AuxSlot
	if auxSlot == 0 {
		auxSlot = 0xffff
	}
	p = add16(p, auxSlot)
	p = add32(p, res.AuxRemaining)
	p = append(p, 0, 0)              // 0x143afc550 extra bytes
	p = append(p, make([]byte, 10)...) // 0x143afc4e0 10-byte details
	return p
}

// PurifyItemRequest is the US v2.24 CMD 204 request body.
// Verified from DFO_unpacked.exe sender 0x143b37e66..0x143b37ee0:
//   u16 target_slot
//   u32 target_template
//   u16 material_slot
//   u32 material_template
type PurifyItemRequest struct {
	TargetSlot       uint16
	TargetTemplate   uint32
	MaterialSlot     uint16
	MaterialTemplate uint32
}

func DecodePurifyItem(p []byte) (PurifyItemRequest, error) {
	var r PurifyItemRequest
	if len(p) < 12 {
		return r, fmt.Errorf("purify item request truncated (%d bytes)", len(p))
	}
	for _, b := range p[12:] {
		if b != 0 {
			return r, fmt.Errorf("non-zero purify item padding")
		}
	}
	r.TargetSlot = binary.LittleEndian.Uint16(p[0:2])
	r.TargetTemplate = binary.LittleEndian.Uint32(p[2:6])
	r.MaterialSlot = binary.LittleEndian.Uint16(p[6:8])
	r.MaterialTemplate = binary.LittleEndian.Uint32(p[8:12])
	if r.TargetTemplate == 0 || r.MaterialTemplate == 0 {
		return r, fmt.Errorf("purify item missing template")
	}
	return r, nil
}

// PurifyItemSuccess encodes ACK 204 consumed by 0x1434b76a5..0x1434b77d9:
//   u8  ok = 1
//   u16 material_slot
//   u32 material_remaining
//   u16 target_slot
//   u8  amplify_type (1..4)
//   u16 amplify_value
func PurifyItemSuccess(materialSlot uint16, materialRemaining uint32, targetSlot uint16, amplifyType byte, amplifyValue uint16) []byte {
	p := []byte{1}
	p = add16(p, materialSlot)
	p = add32(p, materialRemaining)
	p = add16(p, targetSlot)
	p = append(p, amplifyType)
	p = add16(p, amplifyValue)
	return p
}

// InvestAmplifyOptionRequest is the US v2.24 CMD 205 request body.
// Verified from DFO_unpacked.exe sender 0x143b1167d..0x143b117ad:
//   u8  mode (0/1=Silver/Direct, 2=Golden Grimoire)
//   u16 target_slot
//   u32 target_template
//   u16 material_slot
//   u32 material_template
//   u8  chosen_stat (1..4, or 0/6=random)
//   [if mode == 2: u32 name_len + [name_len]byte item_name]
type InvestAmplifyOptionRequest struct {
	Mode             byte
	TargetSlot       uint16
	TargetTemplate   uint32
	MaterialSlot     uint16
	MaterialTemplate uint32
	ChosenStat       byte
	ItemName         string
}

func DecodeInvestAmplifyOption(p []byte) (InvestAmplifyOptionRequest, error) {
	var r InvestAmplifyOptionRequest
	if len(p) < 14 {
		return r, fmt.Errorf("invest amplify request truncated (%d bytes)", len(p))
	}
	r.Mode = p[0]
	r.TargetSlot = binary.LittleEndian.Uint16(p[1:3])
	r.TargetTemplate = binary.LittleEndian.Uint32(p[3:7])
	r.MaterialSlot = binary.LittleEndian.Uint16(p[7:9])
	r.MaterialTemplate = binary.LittleEndian.Uint32(p[9:13])
	r.ChosenStat = p[13]
	off := 14
	if r.Mode == 2 {
		if len(p) < off+4 {
			return r, fmt.Errorf("golden grimoire request missing item name length")
		}
		nameLen := int(binary.LittleEndian.Uint32(p[off : off+4]))
		off += 4
		if nameLen < 0 || nameLen > 512 || off+nameLen > len(p) {
			return r, fmt.Errorf("invalid grimoire item name length %d", nameLen)
		}
		r.ItemName = string(p[off : off+nameLen])
		off += nameLen
	}
	for _, b := range p[off:] {
		if b != 0 {
			return r, fmt.Errorf("non-zero invest amplify padding")
		}
	}
	if r.TargetTemplate == 0 || r.MaterialTemplate == 0 {
		return r, fmt.Errorf("invest amplify missing template")
	}
	return r, nil
}

// InvestAmplifyOptionSuccess encodes ACK 205 consumed by 0x1434b2c9b..0x1434b2e67:
//   u8  ok = 1
//   u8  mode
//   u16 material_slot
//   u32 material_remaining
//   u16 target_slot
//   u8  amplify_type (1..4)
//   u16 amplify_value
//   [if mode == 2: u8 new_upgrade_level]
func InvestAmplifyOptionSuccess(mode byte, materialSlot uint16, materialRemaining uint32, targetSlot uint16, amplifyType byte, amplifyValue uint16, newUpgrade byte) []byte {
	p := []byte{1, mode}
	p = add16(p, materialSlot)
	p = add32(p, materialRemaining)
	p = add16(p, targetSlot)
	p = append(p, amplifyType)
	p = add16(p, amplifyValue)
	if mode == 2 {
		p = append(p, newUpgrade)
	}
	return p
}
