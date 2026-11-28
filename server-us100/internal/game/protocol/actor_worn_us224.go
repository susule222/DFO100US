package protocol

import "fmt"

// The local actor's mode-1 equipment initializer (DFO_unpacked.exe 0x1434eae00)
// covers all 36 worn slots (0..35: avatars 0..11, ordinary gear 12..25,
// creature 26, artifacts 27..29, charm 30, insignia 31, creature skin 32,
// talismans 33..35) using a different schema from mode 0 and the 177-byte
// Space 3 record.
type ActorWornUS224 struct {
	Slot       uint16
	Template   uint32
	Durability uint16
	Upgrade    byte
	Avatar     bool
	CreatureID uint32 // slot 26 ([creature], type 0x1a at 0x1434eb0b1 -> 0x14391da90)
}

func appendActorWornUS224(p []byte, worn []ActorWornUS224) ([]byte, error) {
	var used [36]bool // 0x1434eae7b: cmp rcx, 0x24 (all 36 worn slots 0..35)
	if len(worn) > len(used) {
		return nil, fmt.Errorf("too many equipped items")
	}
	p = append(p, byte(len(worn)))
	for _, w := range worn {
		if int(w.Slot) >= len(used) || used[w.Slot] || w.Template == 0 || w.Template == 0xffffffff || w.Avatar != (w.Slot < 12) {
			return nil, fmt.Errorf("invalid actor equipment at slot %d", w.Slot)
		}
		used[w.Slot] = true
		p = append(p, byte(w.Slot))
		p = add32(add32(p, w.Template), 0) // template, item data
		p = add16(append(p, w.Upgrade), w.Durability)
		p = append(p, 0, 0)                // flags
		p = append(p, make([]byte, 10)...) // ten one-byte item details
		p = add32(add32(p, 0), 0)          // no secondary appearance template or extra data
		p = add16(append(p, 0, 0), 0)
		if w.Avatar {
			// 0x1434eb03d / 0x1434eb060 -> 0x143ad3a70 ([item+0x828] <= 0xb:
			// slots 0..11 including [aurora avatar]=9 and [weapon avatar]=10).
			p = add32(add32(p, 0), 0)
		}
		if w.Slot == 26 {
			// 0x1434eb0b1: cmp dword ptr [rax+0x828], 0x1a ([creature]):
			// reads u32 creatureInstanceID (0x1434eb0cb -> [rbp+0x46] -> 0x14391da90)
			// and u8 flag (0x1434eb14a -> [rbp+0xdf]).
			p = append(add32(p, w.CreatureID), 0)
		}
		p = append(p, 0) // zero option rows
		p = add32(p, 0)
		p = append(p, 0) // nested auxiliary-option count
		p = add16(append(p, 0, 0, 0), 0)
		p = append(p, 0, 0, 0, 0, 0)
		// 2.24 reads ten length-prefixed optional payloads, interspersed
		// with three byte flags and one scalar. Zero lengths, not raw structs.
		p = add32(add32(p, 0), 0)
		p = append(p, 0)
		for i := 0; i < 4; i++ {
			p = add32(p, 0)
		}
		p = add32(p, 0) // scalar
		p = add32(add32(p, 0), 0)
		p = add32(append(p, 0), 0)
		p = add32(append(p, 0), 0)
	}
	return append(p, make([]byte, 4+1+8)...), nil // shared tail after all rows
}
