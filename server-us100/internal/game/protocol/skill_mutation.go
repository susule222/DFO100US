package protocol

import (
	"encoding/binary"
	"fmt"
)

type SkillMove struct{ Tree, From, To byte }

func DecodeSkillMove(p []byte) (SkillMove, error) {
	var r SkillMove
	if len(p) < 7 {
		return r, fmt.Errorf("short skill move")
	}
	// Tree FF means selected tree zero.
	r = SkillMove{p[0], p[1], p[2]}
	if r.Tree == 255 {
		r.Tree = 0
	}
	if r.Tree != 0 || int32(binary.LittleEndian.Uint32(p[3:])) != -1 {
		return r, fmt.Errorf("unsupported skill move context")
	}
	// US100 appends a zero mode byte before the digest; older drags use
	// seven semantic bytes. Both frames pad to 16, so check the digest.
	if len(p) == 16 && p[7] == 0 && digestRequestTail(p, 8, 16) == nil {
		return r, nil
	}
	return r, digestRequestTail(p, 7, 16)
}
func SkillMoveSuccess(r SkillMove) []byte { return []byte{1, r.Tree, r.From, r.To} }

type SkillPurchaseEntry struct {
	ID            uint16
	Refund, Delta byte
}
type SkillPurchase struct {
	Tree    byte
	Entries []SkillPurchaseEntry
}

func DecodeSkillPurchase(p []byte) (SkillPurchase, error) {
	var r SkillPurchase
	if len(p) < 6 {
		return r, fmt.Errorf("short skill purchase")
	}
	r.Tree = p[0]
	if r.Tree == 255 {
		r.Tree = 0
	}
	n := int(p[1])
	end := 2 + n*4
	if r.Tree != 0 || n == 0 || n > 128 || len(p) < end+1 {
		return r, fmt.Errorf("unsupported skill purchase")
	}
	for i := 2; i < end; i += 4 {
		v := SkillPurchaseEntry{binary.LittleEndian.Uint16(p[i:]), p[i+2], p[i+3]}
		if v.ID == 0 || v.Delta == 0 || v.Refund > 1 {
			return r, fmt.Errorf("invalid learning delta/refund")
		}
		r.Entries = append(r.Entries, v)
	}
	// US100 manual learning writes one zero mode byte before the digest.
	// Both layouts can have the same padded length; verify the digest at
	// its candidate boundary instead of reading its bytes as mode flags.
	short := end + 1
	if p[end] == 0 && len(p) == (short+4+7)/8*8 && short <= 400 && digestRequestTail(p, short, 8) == nil {
		return r, nil
	}
	if len(p) < end+4 {
		return r, fmt.Errorf("incomplete skill purchase tail")
	}
	for _, b := range p[end : end+4] {
		if b != 0 {
			return r, fmt.Errorf("skill preset/talisman mode pending")
		}
	}
	return r, digestRequestTail(p, end+4, 8)
}
func SkillPurchaseSuccess(tree byte, sp, tp uint16, entries []LearnedSkill) ([]byte, error) {
	if tree > 1 || len(entries) > 255 {
		return nil, fmt.Errorf("invalid skill purchase response")
	}
	p := add16(add16([]byte{1, tree}, sp), tp)
	p = append(p, byte(len(entries)))
	for _, s := range entries {
		slot := s.Slot
		if slot == 65535 {
			slot = 255
		}
		if slot > 255 || s.ID == 0 {
			return nil, fmt.Errorf("invalid learned skill response")
		}
		p = append(p, byte(slot))
		p = add16(p, s.ID)
		p = append(p, s.Level, 0)
	}
	// Keep the current response tail; the reference90 short response lacks
	// these fields. In the local v224 image the named BUY_SKILL handler is
	// RVA 0x349e5a0, registered on the CMD map (not NOTI 29 / START_MAP).
	return append(p, 0, 0, 0), nil
}
