package protocol

import "fmt"

// The local actor's mode-1 equipment initializer is a different schema
// from mode 0 (appearance), roster, and the 177-byte inventory record.
type ActorWornUS224 struct {
	Slot       uint16
	Template   uint32
	Durability uint16
	Upgrade    byte
	Avatar     bool
}

func appendActorWornUS224(p []byte, worn []ActorWornUS224) ([]byte, error) {
	var used [26]bool // supported persisted avatar/ordinary equipment slots
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
			p = add32(add32(p, 0), 0)
		} // empty emblem and avatar extras
		// No type-26 special equipment in the supported slot domain.
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
