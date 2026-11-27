package protocol

import (
	"encoding/binary"
	"fmt"
)

// DisjointItem is one inventory slot nominated by the client in CMD 26.
type DisjointItem struct {
	Slot     uint16 `json:"slot"`
	Template uint32 `json:"template"`
}

// DisjointRequest is the US v2.24 CMD 26 (equipment/avatar disassembly) request.
// Verified from DFO_unpacked.exe senders at 0x143b36e40 and 0x143b36f7d:
//   u8  space (0=ordinary equipment bag, 1=avatar bag)
//   u16 disassembler_id (0xffff for NPC disassembler, otherwise owner actor ID)
//   u8  count
//   count * (u16 slot, u32 template_id)
// followed by zero cipher-block padding.
type DisjointRequest struct {
	Space          byte           `json:"space"`
	DisassemblerID uint16         `json:"disassembler_id"`
	Items          []DisjointItem `json:"items"`
}

// DisjointReward is one material grant entry echoed in ACK 26.
// Verified from DFO_unpacked.exe ACK 26 handler (0x1434a5710):
//   u16 dst_slot
//   u32 template_id
//   u32 delta_count (added to dst_slot via stackable vtable+0xb0 at 0x143250e40)
type DisjointReward struct {
	Slot     uint16 `json:"slot"`
	Template uint32 `json:"template"`
	Count    uint32 `json:"count"`
}

// DecodeDisjoint decodes a decrypted US v2.24 CMD 26 request body.
func DecodeDisjoint(p []byte) (DisjointRequest, error) {
	var req DisjointRequest
	if len(p) < 4 {
		return req, fmt.Errorf("disjoint request truncated (%d bytes)", len(p))
	}
	space := p[0]
	if space != 0 && space != 1 {
		return req, fmt.Errorf("unsupported disjoint inventory space %d", space)
	}
	disassemblerID := binary.LittleEndian.Uint16(p[1:3])
	count := int(p[3])
	if count == 0 {
		return req, fmt.Errorf("empty disjoint item list")
	}
	need := 4 + count*6
	if len(p) < need || len(p) > need+16 {
		return req, fmt.Errorf("unexpected disjoint request length %d for %d item(s)", len(p), count)
	}
	for _, b := range p[need:] {
		if b != 0 {
			return req, fmt.Errorf("non-zero disjoint request padding")
		}
	}
	items := make([]DisjointItem, 0, count)
	seen := make(map[uint16]bool, count)
	for i := 0; i < count; i++ {
		off := 4 + i*6
		slot := binary.LittleEndian.Uint16(p[off : off+2])
		tpl := binary.LittleEndian.Uint32(p[off+2 : off+6])
		if space == 0 && slot == 0 {
			return req, fmt.Errorf("disjoint slot 0 is reserved")
		}
		if tpl == 0 {
			return req, fmt.Errorf("disjoint item at slot %d has zero template", slot)
		}
		if seen[slot] {
			return req, fmt.Errorf("duplicate disjoint slot %d", slot)
		}
		seen[slot] = true
		items = append(items, DisjointItem{Slot: slot, Template: tpl})
	}
	return DisjointRequest{
		Space:          space,
		DisassemblerID: disassemblerID,
		Items:          items,
	}, nil
}

// DisjointSuccess encodes the US v2.24 ACK 26 success payload consumed by 0x1434a5710:
//   u8  result = 1
//   u8  disjointed_count
//   disjointed_count * u16 slot
//   u8  space
//   u16 disassembler_id
//   u8  reward_count
//   reward_count * (u16 dst_slot, u32 template_id, u32 delta_count)
func DisjointSuccess(req DisjointRequest, rewards []DisjointReward) ([]byte, error) {
	if len(req.Items) == 0 || len(req.Items) > 255 {
		return nil, fmt.Errorf("invalid disjoint item count %d", len(req.Items))
	}
	if len(rewards) > 255 {
		return nil, fmt.Errorf("too many disjoint rewards %d", len(rewards))
	}
	p := make([]byte, 0, 6+len(req.Items)*2+len(rewards)*10)
	p = append(p, 1, byte(len(req.Items)))
	for _, it := range req.Items {
		p = add16(p, it.Slot)
	}
	p = append(p, req.Space)
	p = add16(p, req.DisassemblerID)
	p = append(p, byte(len(rewards)))
	for _, r := range rewards {
		if r.Template == 0 || r.Count == 0 {
			return nil, fmt.Errorf("invalid disjoint reward at slot %d", r.Slot)
		}
		p = add16(p, r.Slot)
		p = add32(p, r.Template)
		p = add32(p, r.Count)
	}
	return p, nil
}

// DisjointRefused encodes the US v2.24 ACK 26 failure payload consumed by 0x1434a5feb.
// Code 22 signals bag full (0x2fa); code 4 is the generic refusal (0x2fc).
func DisjointRefused(code uint16) []byte {
	if code == 0 {
		code = 4
	}
	return Refusal(code)
}
