package protocol

import (
	"encoding/binary"
	"fmt"
)

// EntryBasicProbe is the client's minimum actor-information notification.
// The US100 (2.24) build reads the fixed block before the actor id as 127
// bytes; the 2.38 build used 160. Crash dump 2026-09-17: with the 160-byte
// block the id was read as 0 and the enter-world lookup of the selected
// actor id failed with an access violation at DFO.exe+0x4F1F2A0.
// Native row reader: 0x143807f80 (reads block, then u16 actor id).
type EntryBasicProbe struct {
	ActorServerID  uint16
	Context        [2]byte
	Fame           uint32
	NeoPremiumKind byte // 0x143805930: active Neo Premium kind (0x75/0x76/0x78) sets [userInfo+0x35c]=1 so [Rental Service] gear is wearable
	Character      CharacterRow
}

const (
	fixedRowBlockV224        = 127
	fixedRowBlockFameOffset  = 0x77 // 119: 0x149477577 -> 0x143f0b9f0(actor, fame)
	mode0AppearanceSlotLimit = 36
	mode0AppearanceRowBytes  = 31
)

// WithUserInfoContext stamps the gateway's [serverID, channelID] context bytes
// into a NOTI 2 (USERINFO) payload header so the client's local-channel checks
// (0x143801ca0 mode 0, 0x143802180 mode 1, 0x143801d97 mode 2, 0x143802260 mode 6)
// match g_curServerID (0x149787cb8) and g_curChannelID (0x149787cc0).
func WithUserInfoContext(payload []byte, context [2]byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	out := append([]byte(nil), payload...)
	switch out[0] {
	case 2:
		if len(out) >= 3 {
			out[1], out[2] = context[0], context[1]
		}
	case 0, 1, 6:
		if len(out) >= 5 {
			out[3], out[4] = context[0], context[1]
		}
	}
	return out
}

func UserInfoBasicProbe(s EntryBasicProbe) ([]byte, error) {
	r := s.Character
	if s.ActorServerID == 0 || s.ActorServerID == 0xffff || r.Level == 0 {
		return nil, fmt.Errorf("invalid entry actor identity or level")
	}
	if _, _, err := parseName(addName(nil, r.Name)); err != nil {
		return nil, err
	}
	if len(r.Equipment) > mode0AppearanceSlotLimit {
		return nil, fmt.Errorf("mode-0 appearance row count %d exceeds %d", len(r.Equipment), mode0AppearanceSlotLimit)
	}
	seenSlots := make(map[byte]bool, len(r.Equipment))
	for _, eq := range r.Equipment {
		if eq.Slot >= mode0AppearanceSlotLimit || eq.Item == 0 || seenSlots[eq.Slot] {
			return nil, fmt.Errorf("invalid or duplicate mode-0 appearance slot %d item %d", eq.Slot, eq.Item)
		}
		seenSlots[eq.Slot] = true
	}

	p := append(add16([]byte{0}, 1), s.Context[:]...)
	// 0x143807f80 reads this 127-byte fixed block into 0x149477500 first,
	// then the u16 actor server id. Offset 0x77 carries Adventurer Fame.
	fixed := make([]byte, fixedRowBlockV224)
	binary.LittleEndian.PutUint32(fixed[fixedRowBlockFameOffset:], s.Fame)
	p = append(p, fixed...)
	p = addName(add16(p, s.ActorServerID), r.Name)

	// Native 0x1438081cf..0x14380847e reads:
	//   u8 job ([tmp+0x08]),
	//   u8 grow_packed (low 4 bits -> [tmp+0x0c] grow, bits 4..6 -> [tmp+0x10] second_grow),
	//   u8 level ([tmp+0x18]), u8 ([tmp+0x314]), u8 ([tmp+0x20]).
	p = append(p, r.Profession, r.Advancement&0x0f, r.Level, 0, 0)

	// Native 0x143803660 reads u8 count followed by 31 bytes per equipped visual
	// slot (when dye length at +5 is 0):
	//   u8 slot, u32 itemID, u32 dyeLen(0), u8 upgradeFlag(0), u8(0), u8(0),
	//   [10]byte(0), u32 lookTemplate(0), u32(0), u8(0).
	p = append(p, byte(len(r.Equipment)))
	for _, eq := range r.Equipment {
		p = append(p, eq.Slot)
		p = add32(p, eq.Item)
		p = add32(p, 0) // 0x143a4cdb0 -> 0x144e6eda0: dye length 0
		p = append(p, 0, 0, 0)
		p = append(p, make([]byte, 10)...)
		p = add32(p, 0) // look template (0 keeps default -1)
		p = add32(p, 0)
		p = append(p, 0)
	}

	// Exact US100 (2.24) tail consumed by 0x14380849e..0x143809e00 (117 bytes).
	p = add32(p, 0)              // 0x14380849e -> [tmp+0x268]
	p = append(p, 0, 0, 0, 0)    // 0x1438084bc, 0x1438084db, 0x1438084fa, 0x14380850c
	p = add32(add32(p, 0), 0)    // 0x143808516, 0x143808550
	p = append(p, 0)             // 0x143808594
	p = add32(p, 0)              // 0x1438085d4
	p = append(p, 0)             // 0x143808618 -> [tmp+0x344]
	p = append(addName(add32(p, 0), ""), 0) // 0x143803320: u32 + str + u8
	p = append(p, s.NeoPremiumKind)         // 0x143805930: u8 ([userInfo+0x430]=kind, [userInfo+0x35c]=kind!=0)
	p = add32(add32(p, 0), 0)               // 0x143808645, 0x14380865e
	p = add16(add32(addName(p, ""), 0), 0)  // 0x143803e00: str + u32 + u16
	p = add32(p, 0)                         // 0x143808680
	p = append(p, 1, 0, 0)                  // 0x1438086da ([tmp+0x370]=1), 0x1438086ff, 0x14380870e
	p = add32(append(p, 0), 0)              // 0x143803b10: u8 + u32
	p = append(p, 0)                        // 0x14380877d
	p = add32(add32(p, 0), 0)               // 0x143808787, 0x1438087b8
	p = add16(p, 0)                         // 0x1438087d2
	p = add16(append(p, 0, 0), 0)           // 0x143803f90: u8 + u8 + u16
	p = append(add32(p, 0), make([]byte, 8)...) // 0x1438087f5 u32 + 0x143808804 raw8
	p = append(p, 1)                            // 0x14380ace0: native initialized flag 1 ([tmp+0x371]=1)
	p = add32(p, 0)                             // 0x1438093a0
	p = append(p, 0)                            // 0x1438093b4
	p = add16(p, 0)                             // 0x1438093d7
	p = append(p, 0)                            // 0x143809405
	p = append(add16(p, 0), 0)                  // 0x143805890: u16 + u8 ([info+0x3c4]=0)
	p = add16(p, 0)                             // 0x1438094f0
	p = append(p, 0xff)                         // 0x143804240: native unset sentinel
	p = append(p, 0, 0, 0)                      // 0x14380953a, 0x14380955e, 0x143809582
	p = add32(p, 0)                             // 0x143809c72
	p = append(p, 0, 0, 0, 0, 0, 0)             // 0x143809c9b, 0x143809cc8, 0x143809d8d..0x143809e00
	return p, nil
}

// NameHistoryEntry represents one row in the US100 (2.24) Character Name Change
// History dialog (0x165), populated by NOTI 2 mode 5 (0x14380257c -> 0x142da8480).
type NameHistoryEntry struct {
	Name        string
	ServerGroup byte
	Timestamp   uint32
}

// UserInfoNameHistory encodes US100 (2.24) NOTI 2 mode 5:
//   u8 mode(5), u16 outerCount(1), u8 flag(0), u16 actorID, u8 entryCount,
//   entryCount * [u32 nameLen + nameBytes (<31B per 0x144e6eec0), u8 serverGroup, u32 unixTime].
func UserInfoNameHistory(actorID uint16, entries []NameHistoryEntry) ([]byte, error) {
	if actorID == 0 || actorID == 0xffff {
		return nil, fmt.Errorf("invalid name history actor id")
	}
	if len(entries) > 255 {
		return nil, fmt.Errorf("too many name history entries")
	}
	p := add16([]byte{5}, 1)
	p = append(p, 0)
	p = add16(p, actorID)
	p = append(p, byte(len(entries)))
	for _, entry := range entries {
		nb := []byte(entry.Name)
		if len(nb) == 0 || len(nb) >= 0x1f {
			return nil, fmt.Errorf("invalid name history length %d", len(nb))
		}
		p = add32(p, uint32(len(nb)))
		p = append(p, nb...)
		p = append(p, entry.ServerGroup)
		p = add32(p, entry.Timestamp)
	}
	return p, nil
}
