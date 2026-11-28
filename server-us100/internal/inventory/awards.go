package inventory

import (
	"dfolan/internal/catalog"
	"encoding/json"
	"fmt"
)

type AwardReceipt struct {
	Template, Amount uint32
	Slots            []uint16
}
type Awarder struct {
	Catalog   catalog.LootCatalog
	Rules     BagRules
	Equipment *EquipmentCatalog
}

func repeatTemplate(id, amount uint32) []uint32 {
	ids := make([]uint32, amount)
	for i := range ids {
		ids[i] = id
	}
	return ids
}

func (a *Awarder) Grant(raw json.RawMessage, id, amount uint32) (json.RawMessage, AwardReceipt, error) {
	r := AwardReceipt{Template: id, Amount: amount}
	if a == nil || a.Catalog.Source.Checksum != a.Rules.Source {
		return nil, r, fmt.Errorf("inventory award source missing")
	}
	b, e := ReadBag(raw)
	if e != nil {
		return nil, r, e
	}
	switch {
	case IsCreatureTemplate(id):
		if amount == 0 || amount > uint32(CreatureSlotEnd-CreatureSlotStart+1) {
			return nil, r, fmt.Errorf("invalid creature award")
		}
		b, r.Slots, e = b.AddCreatures(repeatTemplate(id, amount))
	case IsCreatureArtifactTemplate(id):
		if amount == 0 || amount > uint32(ArtifactSlotEnd-ArtifactSlotStart+1) {
			return nil, r, fmt.Errorf("invalid creature artifact award")
		}
		b, r.Slots, e = b.AddCreatureArtifacts(repeatTemplate(id, amount))
	case IsCreatureStackableTemplate(id):
		var slot uint16
		b, slot, e = b.AddCreatureItems(id, amount)
		r.Slots = []uint16{slot}
	case IsInsigniaTemplate(id):
		if amount == 0 || amount > uint32(InsigniaSlotEnd-InsigniaSlotStart+1) {
			return nil, r, fmt.Errorf("invalid insignia award")
		}
		b, r.Slots, e = b.AddInsignias(repeatTemplate(id, amount))
	case IsFlagGemTemplate(id):
		var slot uint16
		b, slot, e = b.AddGems(id, amount)
		r.Slots = []uint16{slot}
	case IsRuneTemplate(id):
		b, r.Slots, e = b.AddRunes(id, amount)
	case IsTalismanTemplate(id) || (a.Equipment != nil && a.Equipment.IsTalisman(id)):
		if amount == 0 || amount > uint32(TalismanSlotCount) {
			return nil, r, fmt.Errorf("invalid talisman award")
		}
		b, r.Slots, e = b.AddTalismans(repeatTemplate(id, amount))
	case IsEmblemTemplate(id):
		limit := uint32(1000)
		if amount > limit {
			limit = amount
		}
		local := catalog.LootCatalog{
			Source: a.Catalog.Source,
			Items: map[uint32]catalog.LootItem{
				id: {ID: id, Kind: "stackable", StackableType: EmblemBagCategory, StackLimit: limit},
			},
		}
		var slot uint16
		b, slot, e = b.Add(local, a.Rules, id, amount)
		r.Slots = []uint16{slot}
	case id == 0 || a.Catalog.Items[id].Kind == "stackable":
		var slot uint16
		b, slot, e = b.Add(a.Catalog, a.Rules, id, amount)
		r.Slots = []uint16{slot}
	default:
		if a.Equipment == nil || a.Equipment.Source.Checksum != a.Rules.Source {
			return nil, r, fmt.Errorf("equipment award source missing")
		}
		b, r.Slots, e = b.AddEquipment(a.Equipment, a.Rules.EquipmentSlots, id, amount)
	}
	if e != nil {
		return nil, r, e
	}
	out, e := SaveBag(raw, b)
	return out, r, e
}
