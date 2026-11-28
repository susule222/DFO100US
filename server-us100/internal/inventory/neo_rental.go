package inventory

import (
	"dfolan/internal/catalog"
	_ "embed"
	"encoding/json"
	"fmt"
)

// US100 etc/pcbangrentalservice.etc and its list/equipment.lst references.
// Embed the subset rather than replacing launcher-generated drop catalogs.
//
//go:embed neo_rentals_us100.json
var neoRentalData []byte
var neoRental = func() struct {
	Source string
	Groups map[string][]uint32
	Rows   map[uint32]EquipmentDefinition
} { var v struct {
	Source string
	Groups map[string][]uint32
	Rows   map[uint32]EquipmentDefinition
}; if e := json.Unmarshal(neoRentalData, &v); e != nil {
	panic(e)
}; return v }()

func (c *EquipmentCatalog) includeNeoRentals() error {
	if c.Source.Checksum != neoRental.Source {
		return nil
	}
	for id, d := range neoRental.Rows {
		if _, ok := c.index[id]; !ok {
			c.index[id] = d
		}
		if _, e := c.Reward(id); e != nil {
			return e
		}
	}
	return nil
}
func activeLoans(items []BagEquipment, now int64) []BagEquipment {
	kept := items[:0]
	for _, it := range items {
		if it.RentalUntil != 0 && it.RentalUntil <= now {
			continue
		}
		kept = append(kept, it)
	}
	return kept
}
// RemovedRental identifies a previously rented Neo weapon removed when switching
// or re-renting via CMD 1200 (ACK 1200 @ DFO_unpacked.exe 0x142cf7c90):
// Space 0 = Equipment bag (slots 9..64), Space 3 = Worn (slot 12).
type RemovedRental struct {
	Space byte
	Slot  uint16
}

func (b Bag) NeoWeapon(job string, choice byte, until int64, c *EquipmentCatalog, r BagRules) (Bag, uint16, error) {
	nb, slot, _, e := b.ReplaceNeoWeapon(job, choice, until, c, r)
	return nb, slot, e
}

// ReplaceNeoWeapon removes any existing Neo Premium rental weapon held in the
// character's Equipment bag (Space 0) or Worn slots (Space 3), then grants the
// newly chosen Phoenix rental weapon into a free Equipment bag slot.
// Matches native US100 ACK 1200 (0x142cf7c90), which carries a list of removed
// (space, slot) pairs so players can switch rental weapons freely without error.
func (b Bag) ReplaceNeoWeapon(job string, choice byte, until int64, c *EquipmentCatalog, r BagRules) (Bag, uint16, []RemovedRental, error) {
	choices := neoRental.Groups[job]
	if c == nil || c.Source.Checksum != neoRental.Source || r.Source != neoRental.Source || int(choice) >= len(choices) || until <= 0 {
		return b, 0, nil, fmt.Errorf("unsupported rental selection")
	}
	var removed []RemovedRental
	keptEquip := make([]BagEquipment, 0, len(b.Equipment))
	for _, it := range b.Equipment {
		if it.RentalUntil != 0 {
			removed = append(removed, RemovedRental{Space: 0, Slot: it.Slot})
			continue
		}
		keptEquip = append(keptEquip, it)
	}
	keptWorn := make([]BagEquipment, 0, len(b.Worn))
	for _, it := range b.Worn {
		if it.RentalUntil != 0 {
			removed = append(removed, RemovedRental{Space: 3, Slot: it.Slot})
			continue
		}
		keptWorn = append(keptWorn, it)
	}
	b.Equipment = keptEquip
	b.Worn = keptWorn

	b, slots, e := b.AddEquipment(c, r.EquipmentSlots, choices[choice], 1)
	if e != nil {
		return b, 0, nil, e
	}
	for i := range b.Equipment {
		if b.Equipment[i].Slot == slots[0] {
			b.Equipment[i].RentalUntil = until
		}
	}
	return b, slots[0], removed, nil
}
func (b Bag) NeoGift(src catalog.LootCatalog, r BagRules) (Bag, uint16, error) {
	if src.Source.Checksum != neoRental.Source || r.Source != neoRental.Source {
		return b, 0, fmt.Errorf("unsupported gift source")
	}
	// Source 50002547 is a booster-selection consumable. Use the normal consumable
	// partition without adding the membership-only gift to the world loot table.
	src.Items = map[uint32]catalog.LootItem{50002547: {ID: 50002547, Kind: "stackable", StackableType: "[throw]", StackLimit: 1}}
	return b.Add(src, r, 50002547, 1)
}
