package protocol

import (
	"fmt"
	"sort"
)

// AccountMaterialEntry is one (template_id, absolute_amount) row in NOTI 1140.
type AccountMaterialEntry struct {
	Template uint32
	Amount   uint32
}

// US224AccountMaterials encodes US v2.24 NOTI 1140 (Account Material / Cube &
// Soul Warehouse sync), verified from DFO_unpacked.exe 0x1434f4960:
//   u8  mode   (0 = reset/clear all known IDs via 0x1428b22d0 before applying;
//               1 = incremental merge/update)
//   u32 count
//   count * (u32 template_id, u32 absolute_amount)  // set via 0x1428b24c0
// Followed automatically by UI window 0x175 refresh (0x1429c5760).
func US224AccountMaterials(reset bool, entries []AccountMaterialEntry) ([]byte, error) {
	mode := byte(1)
	if reset {
		mode = 0
	}
	p := make([]byte, 0, 5+len(entries)*8)
	p = append(p, mode)
	p = add32(p, uint32(len(entries)))
	seen := make(map[uint32]bool, len(entries))
	for _, e := range entries {
		if e.Template == 0 {
			return nil, fmt.Errorf("account material entry has zero template")
		}
		if seen[e.Template] {
			return nil, fmt.Errorf("duplicate account material template %d", e.Template)
		}
		seen[e.Template] = true
		p = add32(p, e.Template)
		p = add32(p, e.Amount)
	}
	return p, nil
}

// US224AccountMaterialsFromMap encodes a template->amount map deterministically
// sorted by template ID for NOTI 1140.
func US224AccountMaterialsFromMap(reset bool, amounts map[uint32]uint32) ([]byte, error) {
	ids := make([]uint32, 0, len(amounts))
	for id := range amounts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	entries := make([]AccountMaterialEntry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, AccountMaterialEntry{Template: id, Amount: amounts[id]})
	}
	return US224AccountMaterials(reset, entries)
}
