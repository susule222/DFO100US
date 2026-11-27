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
func (b Bag) NeoWeapon(job string, choice byte, until int64, c *EquipmentCatalog, r BagRules) (Bag, uint16, error) {
	choices := neoRental.Groups[job]
	if c == nil || c.Source.Checksum != neoRental.Source || r.Source != neoRental.Source || int(choice) >= len(choices) || until <= 0 {
		return b, 0, fmt.Errorf("unsupported rental selection")
	}
	for _, items := range [][]BagEquipment{b.Equipment, b.Worn} {
		for _, it := range items {
			if it.RentalUntil != 0 {
				return b, 0, fmt.Errorf("current character already holds a Neo rental")
			}
		}
	}
	b, slots, e := b.AddEquipment(c, r.EquipmentSlots, choices[choice], 1)
	if e != nil {
		return b, 0, e
	}
	for i := range b.Equipment {
		if b.Equipment[i].Slot == slots[0] {
			b.Equipment[i].RentalUntil = until
		}
	}
	return b, slots[0], nil
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
