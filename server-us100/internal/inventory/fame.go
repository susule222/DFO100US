package inventory

import (
	"encoding/json"
	"math"
)

// CNFameSystem formulas recovered from US100 DFO.exe (0x1453d2980, 0x1453ce550,
// 0x1453dadc0) and PVF Etc/FameValueInfo.etc.
var (
	equipRarityFameMult = [8]float32{0.50, 0.55, 0.60, 0.65, 1.00, 0.65, 0.72, 1.25}
	avatarFameCoeffA    = [6]float32{2.0, 2.0, 2.0, 4.0, 6.0, 9.0}
	avatarFameCoeffB    = [6]float32{1.3, 1.3, 1.3, 1.3, 1.49, 1.57}

	titleFameTable = map[int32]uint32{
		1: 315, 2: 397, 3: 491, 4: 601, 5: 731,
		100: 783, 101: 849, 102: 934, 103: 1044, 250: 10000,
	}
	creatureFameTable = map[int32]uint32{
		1: 315, 2: 397, 3: 491, 4: 601, 5: 731, 6: 889,
		100: 752, 101: 778, 102: 805, 103: 832, 104: 860, 105: 889,
		106: 919, 107: 949, 108: 980, 109: 1012, 110: 1044, 111: 1055,
	}
	auraAvatarFameTable = map[int32]uint32{
		1: 140, 2: 170, 3: 209, 4: 260, 5: 325, 6: 380, 100: 464, 201: 545,
	}
	weaponAvatarFameTable = map[int32]uint32{
		1: 72, 2: 94, 3: 166, 100: 94,
	}
)

// AdventurerFame computes total Adventurer Fame for a persisted character state.
// Safe on a nil catalog (avatars and fallback tiers still contribute).
func (c *EquipmentCatalog) AdventurerFame(state json.RawMessage) uint32 {
	if len(state) == 0 {
		return 0
	}
	b, err := ReadBag(state)
	if err != nil {
		return 0
	}
	return c.BagAdventurerFame(b)
}

// BagAdventurerFame sums Adventurer Fame across worn ordinary equipment and
// worn avatars using the client's CNFameSystem formulas.
func (c *EquipmentCatalog) BagAdventurerFame(b Bag) uint32 {
	var total uint32
	for _, item := range b.Worn {
		if item.Template == 0 {
			continue
		}
		var upgrade byte
		if item.RentalUntil > 0 {
			upgrade = 7
		}
		total += c.wornEquipmentFame(item.Slot, item.Template, upgrade)
	}
	for _, item := range b.WornAvatars {
		if item.Template == 0 {
			continue
		}
		total += c.wornAvatarFame(item.Slot, item.Template)
	}
	return total
}

func (c *EquipmentCatalog) wornEquipmentFame(slot uint16, template uint32, upgrade byte) uint32 {
	minLevel, rarity, grade, ok := c.fameMeta(template)
	switch slot {
	case 13: // title
		if ok {
			if v, found := titleFameTable[grade]; found {
				return v
			}
			if v, found := titleFameTable[rarity+1]; found {
				return v
			}
		}
		return titleFameTable[1]
	case 24: // creature
		if ok {
			if v, found := creatureFameTable[grade]; found {
				return v
			}
		}
		return creatureFameTable[1]
	}
	if slot < 12 || slot > 25 {
		return 0
	}
	if !ok {
		return 0
	}
	return OrdinaryEquipmentFame(slot, minLevel, rarity, upgrade)
}

func (c *EquipmentCatalog) wornAvatarFame(slot uint16, template uint32) uint32 {
	_, rarity, grade, ok := c.fameMeta(template)
	switch slot {
	case 9: // aura avatar
		if ok {
			if v, found := auraAvatarFameTable[grade]; found {
				return v
			}
		}
		return auraAvatarFameTable[1]
	case 10: // weapon avatar
		if ok {
			if v, found := weaponAvatarFameTable[grade]; found {
				return v
			}
		}
		return weaponAvatarFameTable[1]
	}
	if slot > 11 {
		return 0
	}
	// Native 0x1453ce550 maps avatar grade (CEquipment+0x14) to fame index:
	//   grade 3 (rare) -> 4 (116)
	//   grade 2 (advanced) -> 3 (88)
	//   else skin (slot 8) -> 1 (69), normal -> 2 (69)
	// Cash-shop avatars absent from equipment.generated.json default to
	// skin (slot 8 -> 69) or advanced avatar (grade 2 -> 88).
	avatarGrade := int32(2)
	if slot == 8 {
		avatarGrade = 1
	}
	if ok {
		if grade > 0 {
			avatarGrade = grade
		} else if rarity >= 0 {
			avatarGrade = rarity
		}
	}
	return AvatarFame(slot, avatarGrade)
}

func (c *EquipmentCatalog) fameMeta(template uint32) (minLevel, rarity, grade int32, ok bool) {
	if c == nil {
		return 0, 0, 0, false
	}
	def, found := c.index[template]
	if !found {
		return 0, 0, 0, false
	}
	if m := def.Fields["[minimum level]"]; len(m) == 1 && m[0].Type == 0 && m[0].Value > 0 {
		minLevel = m[0].Value
	}
	if g := def.Fields["[grade]"]; len(g) == 1 && g[0].Type == 0 && g[0].Value > 0 {
		grade = g[0].Value
	}
	if minLevel == 0 {
		minLevel = grade
	}
	if r := def.Fields["[rarity]"]; len(r) == 1 && r[0].Type == 0 && r[0].Value >= 0 {
		rarity = r[0].Value
	}
	return minLevel, rarity, grade, true
}

// OrdinaryEquipmentFame implements CNFameSystem::GetEquipFame (0x1453d2980 + 0x1453dadc0).
func OrdinaryEquipmentFame(slot uint16, minLevel, rarity int32, upgrade byte) uint32 {
	if slot < 12 || slot > 25 || slot == 13 || slot == 24 {
		return 0
	}
	if rarity < 0 || int(rarity) >= len(equipRarityFameMult) {
		return 0
	}
	if minLevel < 0 {
		minLevel = 0
	}
	tier := (minLevel / 5) + 1
	baseF := float32(math.Pow(0.65, float64(21-tier))) * float32(464.0)
	base := float32(math.Ceil(float64(baseF)))
	itemBase := uint32(math.Ceil(float64(base * equipRarityFameMult[rarity])))
	if upgrade == 0 {
		return itemBase
	}
	var tierMult float32
	switch {
	case upgrade <= 7:
		tierMult = 0.20
	case upgrade <= 9:
		tierMult = 0.30
	case upgrade == 10:
		tierMult = 0.38
	case upgrade == 11:
		tierMult = 0.68
	default:
		tierMult = 1.00
	}
	slotMult := float32(0.34)
	if slot == 12 {
		slotMult = 1.06
	}
	// Normal reinforce multiplier (0.90) matches 0x1453dadc0.
	bonusF := float32(itemBase) * float32(upgrade) * tierMult * (float32(1.0) / float32(12.0)) * float32(0.90) * slotMult
	return itemBase + uint32(math.Ceil(float64(bonusF)))
}

// AvatarFame implements CNFameSystem::GetAvatarFame (0x1453ce550 + 0x1453d2ad4).
func AvatarFame(slot uint16, grade int32) uint32 {
	var idx int
	switch {
	case grade >= 4:
		idx = 5
	case grade == 3:
		idx = 4
	case grade == 2:
		idx = 3
	case slot == 8:
		idx = 1
	default:
		idx = 2
	}
	return uint32(avatarFameCoeffA[idx]*float32(7.3)*avatarFameCoeffB[idx] + float32(51.0))
}
