package inventory

import (
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"time"
)

type BagRules struct {
	Model             string               `json:"model"`
	Source            string               `json:"source"`
	Slots             map[string][2]uint16 `json:"slots"`
	SlotAliases       map[string]string    `json:"slot_aliases,omitempty"`
	MissingStackLimit uint32               `json:"missing_stack_limit"`
	EquipmentSlots    [2]uint16            `json:"equipment_slots,omitempty"`
	// QuickSlots is the belt the client drags consumables onto, below the
	// equipment range. Live capture 20260912T004320 shows CMD19 asking to put
	// the 6003 stack from slot 65 into slot 3; every earlier build refused it
	// because list 0 modelled nothing but the equipment range, and the client
	// then showed its generic "target inventory is full" notice. Slot 0 is a
	// legal belt slot, so this range is not subject to the nonzero check the
	// type ranges use.
	QuickSlots [2]uint16 `json:"quick_slots,omitempty"`
}

// Quick reports whether a slot is part of the quick-use belt.
func (r BagRules) Quick(slot uint16) bool {
	return r.QuickSlots != [2]uint16{} && slot >= r.QuickSlots[0] && slot <= r.QuickSlots[1]
}

func LoadBagRules(p string) (BagRules, error) {
	var r BagRules
	b, e := os.ReadFile(p)
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.Model != "reference90-bag-v1" || len(r.Source) != 64 || r.MissingStackLimit == 0 {
		return r, fmt.Errorf("invalid bag policy")
	}
	seen := map[uint16]bool{}
	if r.QuickSlots != [2]uint16{} {
		v := r.QuickSlots
		if v[0] > v[1] || v[1] > 65534 {
			return r, fmt.Errorf("invalid quick slot range")
		}
		if r.EquipmentSlots != [2]uint16{} && v[1] >= r.EquipmentSlots[0] {
			return r, fmt.Errorf("quick slots overlap the equipment bag")
		}
		for n := uint32(v[0]); n <= uint32(v[1]); n++ {
			seen[uint16(n)] = true
		}
	}
	if r.EquipmentSlots != [2]uint16{} {
		v := r.EquipmentSlots
		if v[0] == 0 || v[0] > v[1] || v[1] > 65534 {
			return r, fmt.Errorf("invalid equipment bag range")
		}
		for n := uint32(v[0]); n <= uint32(v[1]); n++ {
			seen[uint16(n)] = true
		}
	}
	for _, v := range r.Slots {
		if v[0] == 0 || v[0] > v[1] || v[1] > 65534 {
			return r, fmt.Errorf("invalid bag slot range")
		}
		for n := uint32(v[0]); n <= uint32(v[1]); n++ {
			if seen[uint16(n)] {
				return r, fmt.Errorf("overlapping bag ranges")
			}
			seen[uint16(n)] = true
		}
	}
	for alias, target := range r.SlotAliases {
		if _, found := r.Slots[alias]; found || r.Slots[target][0] == 0 {
			return r, fmt.Errorf("invalid bag category alias %s", alias)
		}
	}
	return r, nil
}

const AvatarBagCapacity uint16 = 280

// Talisman & Rune Inventory (space 0x2b) slot layout verified from DFO_unpacked.exe:
//   0..79    (0x00..0x4f): 80 Talisman bag slots (0x1416c0c90(0)=0, 0x141406ca0()=0x50)
//   80..319  (0x50..0x13f): 240 Rune bag slots (0x1416c0c90(1)=0x50, 0x1414062d0()=0xf0)
//   320..328 (0x140..0x148): 9 equipped Rune slots (3 per worn talisman 33..35, 0x1414061c0)
const (
	TalismanBagCapacity uint16 = 329 // 0x149
	TalismanSlotCount   uint16 = 80  // 0x50: slots 0..79
	RuneSlotStart       uint16 = 80  // 0x50: slots 80..319
	RuneSlotEnd         uint16 = 319 // 0x13f
	EquippedRuneStart   uint16 = 320 // 0x140: slots 320..328
	EquippedRuneEnd     uint16 = 328 // 0x148
	WornTalismanSlotMin uint16 = 33  // 0x21
	WornTalismanSlotMax uint16 = 35  // 0x23
)

// DefaultTalismanRuneSockets provides 3 valid distinct rune socket color IDs
// (2=Terracotta/Purple, 3=Circle Mage/Blue, 4=Second Pact/Red) when a talisman
// has no persisted socket roll yet.
var DefaultTalismanRuneSockets = [3]byte{2, 3, 4}

// MaxExpandedInventorySlots is the US224 client's maximum ordinary inventory
// expansion (+2 rows = +16 slots per tab, verified at DFO_unpacked.exe
// 0x1439af840 / 0x1439b0e55: stage 1=base 0, stage 2=+8, stage 3=+16).
const MaxExpandedInventorySlots uint16 = 16

type BagItem struct {
	Slot       uint16 `json:"slot"`
	Template   uint32
	Amount     uint32
	SortLocked bool  `json:"sort_locked,omitempty"`
	ItemLock   byte  `json:"item_lock,omitempty"`
	UnlockAt   int64 `json:"unlock_at,omitempty"`
}
type Bag struct {
	Version           string         `json:"version"`
	Gold              uint32         `json:"gold"`
	ExpandedSlots     uint16         `json:"expanded_slots,omitempty"`
	Items             []BagItem      `json:"items"`
	Equipment         []BagEquipment `json:"equipment,omitempty"`
	Worn              []BagEquipment `json:"worn,omitempty"`
	Avatars           []BagEquipment `json:"avatars,omitempty"`
	WornAvatars       []BagEquipment `json:"worn_avatars,omitempty"`
	Creatures         []BagEquipment `json:"creatures,omitempty"`
	CreatureArtifacts []BagEquipment `json:"creature_artifacts,omitempty"`
	CreatureItems     []BagItem      `json:"creature_items,omitempty"`
	Insignias         []BagEquipment `json:"insignias,omitempty"`
	Gems              []BagItem      `json:"gems,omitempty"`
	Talismans         []BagEquipment `json:"talismans,omitempty"`
	Runes             []BagItem      `json:"runes,omitempty"`
}

// ExpandInventory upgrades the character's ordinary inventory expansion slots
// in 8-slot (1-row) steps up to MaxExpandedInventorySlots (16).
// target == 0 means "expand by one stage (+8 slots)"; target == 8 or 16
// enforces the matching sequential stage (1阶段 = 0->8, 2阶段 = 8->16).
func (b *Bag) ExpandInventory(target uint16) (uint16, error) {
	if b.ExpandedSlots >= MaxExpandedInventorySlots {
		return b.ExpandedSlots, fmt.Errorf("inventory is already at maximum capacity")
	}
	next := b.ExpandedSlots + 8
	if target != 0 {
		if target%8 != 0 || target > MaxExpandedInventorySlots {
			return b.ExpandedSlots, fmt.Errorf("invalid inventory expansion target %d", target)
		}
		if target <= b.ExpandedSlots {
			return b.ExpandedSlots, fmt.Errorf("inventory expansion stage already reached")
		}
		if target != next {
			return b.ExpandedSlots, fmt.Errorf("previous inventory expansion stage required")
		}
		next = target
	}
	b.ExpandedSlots = next
	return b.ExpandedSlots, nil
}

func ReadBag(state json.RawMessage) (Bag, error) {
	var fields map[string]json.RawMessage
	var b Bag
	if e := json.Unmarshal(state, &fields); e != nil {
		return b, e
	}
	if fields == nil {
		return b, fmt.Errorf("character state is not an object")
	}
	if p, ok := fields["inventory"]; ok {
		if e := json.Unmarshal(p, &b); e != nil {
			return b, e
		}
		if b.Version != "ordinary-bag-v1" {
			return b, fmt.Errorf("unsupported saved inventory")
		}
	} else {
		b.Version = "ordinary-bag-v1"
	}
	if b.ExpandedSlots > MaxExpandedInventorySlots || b.ExpandedSlots%8 != 0 {
		return b, fmt.Errorf("invalid saved inventory expansion %d", b.ExpandedSlots)
	}
	now := time.Now().Unix()
	b.Equipment = activeLoans(b.Equipment, now)
	b.Worn = activeLoans(b.Worn, now)
	seen := map[uint16]bool{}
	for _, i := range b.Items {
		if i.Slot == 0 || i.Template == 0 || i.Amount == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved inventory row")
		}
		seen[i.Slot] = true
	}
	for _, i := range b.Equipment {
		if i.Slot == 0 || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved equipment")
		}
		seen[i.Slot] = true
	}
	seen = map[uint16]bool{}
	for idx := range b.Worn {
		i := b.Worn[idx]
		validOrdinary := i.Slot >= 12 && i.Slot <= 25
		validCreature := i.Slot == WornCreatureSlot || (i.Slot >= WornArtifactSlotMin && i.Slot <= WornArtifactSlotMax)
		validInsignia := i.Slot == WornInsigniaSlot
		validTalisman := i.Slot >= WornTalismanSlotMin && i.Slot <= WornTalismanSlotMax
		if (!validOrdinary && !validCreature && !validInsignia && !validTalisman) || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved worn equipment")
		}
		if validTalisman && b.Worn[idx].RuneSockets == [3]byte{} {
			b.Worn[idx].RuneSockets = DefaultTalismanRuneSockets
		}
		if i.Slot == WornCreatureSlot {
			if hatched := CreatureEggOutput(b.Worn[idx].Template); hatched != 0 {
				b.Worn[idx].Template = hatched
			}
		}
		seen[i.Slot] = true
	}
	seenAvatar := map[uint16]bool{}
	for _, i := range b.Avatars {
		if i.Slot >= AvatarBagCapacity || i.Template == 0 || seenAvatar[i.Slot] {
			return b, fmt.Errorf("invalid saved avatar item")
		}
		seenAvatar[i.Slot] = true
	}
	seen = map[uint16]bool{}
	for _, i := range b.WornAvatars {
		if i.Slot > 11 || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved worn avatar")
		}
		seen[i.Slot] = true
	}
	seenCreature := map[uint16]bool{}
	for _, i := range b.Creatures {
		if i.Slot > CreatureSlotEnd || i.Template == 0 || seenCreature[i.Slot] {
			return b, fmt.Errorf("invalid saved creature item")
		}
		seenCreature[i.Slot] = true
	}
	for _, i := range b.CreatureArtifacts {
		if i.Slot < ArtifactSlotStart || i.Slot > ArtifactSlotEnd || i.Template == 0 || seenCreature[i.Slot] {
			return b, fmt.Errorf("invalid saved creature artifact item")
		}
		seenCreature[i.Slot] = true
	}
	for _, i := range b.CreatureItems {
		if i.Slot < CreatureItemSlotStart || i.Slot > CreatureItemSlotEnd || i.Template == 0 || i.Amount == 0 || seenCreature[i.Slot] {
			return b, fmt.Errorf("invalid saved creature consumable item")
		}
		seenCreature[i.Slot] = true
	}
	seenInsignia := map[uint16]bool{}
	for _, i := range b.Insignias {
		if i.Slot > InsigniaSlotEnd || i.Template == 0 || seenInsignia[i.Slot] {
			return b, fmt.Errorf("invalid saved insignia item")
		}
		seenInsignia[i.Slot] = true
	}
	for _, i := range b.Gems {
		if i.Slot < GemSlotStart || i.Slot > GemSlotEnd || i.Template == 0 || i.Amount == 0 || seenInsignia[i.Slot] {
			return b, fmt.Errorf("invalid saved flag gem item")
		}
		seenInsignia[i.Slot] = true
	}
	seen = map[uint16]bool{}
	for idx := range b.Talismans {
		i := b.Talismans[idx]
		if i.Slot >= TalismanSlotCount || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved talisman item")
		}
		if b.Talismans[idx].RuneSockets == [3]byte{} {
			b.Talismans[idx].RuneSockets = DefaultTalismanRuneSockets
		}
		seen[i.Slot] = true
	}
	for _, i := range b.Runes {
		if i.Slot < RuneSlotStart || i.Slot >= TalismanBagCapacity || i.Template == 0 || i.Amount == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved rune item")
		}
		seen[i.Slot] = true
	}
	// Auto-migrate any specialized equipment (Avatars/Auras, Talismans, Creatures,
	// Creature Artifacts, Insignias) previously placed in the ordinary Equipment
	// tab (space 0) into their native spaces (1, 0x2b, 7, 0x25).
	if len(b.Equipment) > 0 {
		var keptEquip []BagEquipment
		var migAv, migTal, migCre, migArt, migIns []BagEquipment
		for _, eq := range b.Equipment {
			switch {
			case IsAvatarTemplate(eq.Template):
				migAv = append(migAv, eq)
			case IsTalismanTemplate(eq.Template):
				migTal = append(migTal, eq)
			case IsCreatureTemplate(eq.Template):
				migCre = append(migCre, eq)
			case IsCreatureArtifactTemplate(eq.Template):
				migArt = append(migArt, eq)
			case IsInsigniaTemplate(eq.Template):
				migIns = append(migIns, eq)
			default:
				keptEquip = append(keptEquip, eq)
			}
		}
		placeEquip := func(src []BagEquipment, dst *[]BagEquipment, occ map[uint16]bool, start, end uint16, talisman bool) {
			if len(src) == 0 {
				return
			}
			sort.Slice(src, func(i, j int) bool { return src[i].Slot < src[j].Slot })
			for _, eq := range src {
				placed := false
				for slot := start; slot <= end; slot++ {
					if !occ[slot] {
						occ[slot] = true
						eq.Slot = slot
						eq.Durability = 0
						if talisman && eq.RuneSockets == [3]byte{} {
							eq.RuneSockets = DefaultTalismanRuneSockets
						}
						*dst = append(*dst, eq)
						placed = true
						break
					}
				}
				if !placed {
					keptEquip = append(keptEquip, eq)
				}
			}
		}
		placeEquip(migAv, &b.Avatars, seenAvatar, 0, AvatarBagCapacity-1, false)
		placeEquip(migTal, &b.Talismans, seen, 0, TalismanSlotCount-1, true)
		placeEquip(migCre, &b.Creatures, seenCreature, CreatureSlotStart, CreatureSlotEnd, false)
		placeEquip(migArt, &b.CreatureArtifacts, seenCreature, ArtifactSlotStart, ArtifactSlotEnd, false)
		placeEquip(migIns, &b.Insignias, seenInsignia, InsigniaSlotStart, InsigniaSlotEnd, false)
		b.Equipment = keptEquip
	}
	// Auto-migrate any specialized items previously placed in Space 0 stackable tabs
	// (Avatars/Auras, Talismans, Creatures/Eggs, Artifacts, Creature Consumables,
	// Insignias, Flag Gems, Runes, misaligned Emblems, and ordinary equipment/titles
	// from older booster catalogs) into their native spaces/tabs.
	if len(b.Items) > 0 {
		var keptItems []BagItem
		var migAv, migTal, migCre, migArt, migIns, migOrdEq []BagItem
		var migCreStk, migGems, migRunes, migratedEmblems []BagItem
		occupiedSpace0 := make(map[uint16]bool, len(b.Items)+len(b.Equipment))
		for _, eq := range b.Equipment {
			occupiedSpace0[eq.Slot] = true
		}
		for _, it := range b.Items {
			switch {
			case IsAvatarTemplate(it.Template):
				migAv = append(migAv, it)
			case IsTalismanTemplate(it.Template):
				migTal = append(migTal, it)
			case IsCreatureTemplate(it.Template):
				migCre = append(migCre, it)
			case IsCreatureArtifactTemplate(it.Template):
				migArt = append(migArt, it)
			case IsCreatureStackableTemplate(it.Template):
				migCreStk = append(migCreStk, it)
			case IsInsigniaTemplate(it.Template):
				migIns = append(migIns, it)
			case IsFlagGemTemplate(it.Template):
				migGems = append(migGems, it)
			case IsRuneTemplate(it.Template):
				migRunes = append(migRunes, it)
			case IsEmblemTemplate(it.Template) && (it.Slot < EmblemSlotStart || it.Slot > EmblemSlotEnd):
				migratedEmblems = append(migratedEmblems, it)
			case IsKnownOrdinaryEquipment(it.Template):
				migOrdEq = append(migOrdEq, it)
			default:
				keptItems = append(keptItems, it)
				occupiedSpace0[it.Slot] = true
			}
		}
		placeItemAsEquip := func(src []BagItem, dst *[]BagEquipment, occ map[uint16]bool, start, end uint16) {
			if len(src) == 0 {
				return
			}
			sort.Slice(src, func(i, j int) bool { return src[i].Slot < src[j].Slot })
			for _, it := range src {
				count := it.Amount
				if count == 0 {
					count = 1
				}
				rem := uint32(0)
				for c := uint32(0); c < count; c++ {
					placed := false
					for slot := start; slot <= end; slot++ {
						if !occ[slot] {
							occ[slot] = true
							*dst = append(*dst, BagEquipment{
								Slot:       slot,
								Template:   it.Template,
								SortLocked: it.SortLocked,
								ItemLock:   it.ItemLock,
								UnlockAt:   it.UnlockAt,
							})
							placed = true
							break
						}
					}
					if !placed {
						rem++
					}
				}
				if rem > 0 {
					it.Amount = rem
					keptItems = append(keptItems, it)
				}
			}
		}
		placeItemStack := func(src []BagItem, dst *[]BagItem, occ map[uint16]bool, start, end uint16, stackable bool) {
			if len(src) == 0 {
				return
			}
			sort.Slice(src, func(i, j int) bool { return src[i].Slot < src[j].Slot })
			for _, it := range src {
				if stackable {
					placed := false
					for i := range *dst {
						cur := &(*dst)[i]
						if cur.Template == it.Template && cur.Slot >= start && cur.Slot <= end && uint64(cur.Amount)+uint64(it.Amount) <= 1000 {
							cur.Amount += it.Amount
							placed = true
							break
						}
					}
					if !placed {
						for slot := start; slot <= end; slot++ {
							if !occ[slot] {
								occ[slot] = true
								it.Slot = slot
								*dst = append(*dst, it)
								placed = true
								break
							}
						}
					}
					if !placed {
						keptItems = append(keptItems, it)
					}
					continue
				}
				// Non-stackable (one per slot, e.g., Runes)
				rem := uint32(0)
				for c := uint32(0); c < it.Amount; c++ {
					placed := false
					for slot := start; slot <= end; slot++ {
						if !occ[slot] {
							occ[slot] = true
							*dst = append(*dst, BagItem{Slot: slot, Template: it.Template, Amount: 1})
							placed = true
							break
						}
					}
					if !placed {
						rem++
					}
				}
				if rem > 0 {
					it.Amount = rem
					keptItems = append(keptItems, it)
				}
			}
		}
		placeItemAsEquip(migAv, &b.Avatars, seenAvatar, 0, AvatarBagCapacity-1)
		if len(migTal) > 0 {
			before := len(b.Talismans)
			placeItemAsEquip(migTal, &b.Talismans, seen, 0, TalismanSlotCount-1)
			for i := before; i < len(b.Talismans); i++ {
				if b.Talismans[i].RuneSockets == [3]byte{} {
					b.Talismans[i].RuneSockets = DefaultTalismanRuneSockets
				}
			}
		}
		placeItemAsEquip(migCre, &b.Creatures, seenCreature, CreatureSlotStart, CreatureSlotEnd)
		placeItemAsEquip(migArt, &b.CreatureArtifacts, seenCreature, ArtifactSlotStart, ArtifactSlotEnd)
		placeItemAsEquip(migIns, &b.Insignias, seenInsignia, InsigniaSlotStart, InsigniaSlotEnd)
		if len(migOrdEq) > 0 {
			sort.Slice(migOrdEq, func(i, j int) bool { return migOrdEq[i].Slot < migOrdEq[j].Slot })
			for _, it := range migOrdEq {
				count := it.Amount
				if count == 0 {
					count = 1
				}
				dur := OrdinaryEquipmentDurability(it.Template)
				rem := uint32(0)
				for c := uint32(0); c < count; c++ {
					placed := false
					for slot := uint16(9); slot <= 64; slot++ {
						if !occupiedSpace0[slot] {
							occupiedSpace0[slot] = true
							b.Equipment = append(b.Equipment, BagEquipment{
								Slot:       slot,
								Template:   it.Template,
								Durability: dur,
								SortLocked: it.SortLocked,
								ItemLock:   it.ItemLock,
								UnlockAt:   it.UnlockAt,
							})
							placed = true
							break
						}
					}
					if !placed {
						rem++
					}
				}
				if rem > 0 {
					it.Amount = rem
					keptItems = append(keptItems, it)
				}
			}
		}
		placeItemStack(migCreStk, &b.CreatureItems, seenCreature, CreatureItemSlotStart, CreatureItemSlotEnd, true)
		placeItemStack(migGems, &b.Gems, seenInsignia, GemSlotStart, GemSlotEnd, true)
		placeItemStack(migRunes, &b.Runes, seen, RuneSlotStart, RuneSlotEnd, false)
		if len(migratedEmblems) > 0 {
			sort.Slice(migratedEmblems, func(i, j int) bool { return migratedEmblems[i].Slot < migratedEmblems[j].Slot })
			for _, em := range migratedEmblems {
				placed := false
				for i := range keptItems {
					cur := &keptItems[i]
					if cur.Template == em.Template && cur.Slot >= EmblemSlotStart && cur.Slot <= EmblemSlotEnd && uint64(cur.Amount)+uint64(em.Amount) <= 1000 {
						cur.Amount += em.Amount
						placed = true
						break
					}
				}
				if !placed {
					for slot := EmblemSlotStart; slot <= EmblemSlotEnd; slot++ {
						if !occupiedSpace0[slot] {
							occupiedSpace0[slot] = true
							em.Slot = slot
							keptItems = append(keptItems, em)
							placed = true
							break
						}
					}
				}
				if !placed {
					keptItems = append(keptItems, em)
				}
			}
		}
		b.Items = keptItems
	}
	b.ensureCreatureIDs()
	return b, nil
}

// ensureCreatureIDs deterministically assigns unique non-zero CreatureIDs to
// every creature in Space 3 slot 26 and Space 7 (0..159). US100's creature
// renderer (0x14400b150) reads ItemExtra+0x00 (decoded from raw[6:10]) and looks
// it up in the NOTI 105 CreatureMap (0x1497cf930); ID 0 causes the sprite to be
// skipped completely.
func (b *Bag) ensureCreatureIDs() {
	used := map[uint32]bool{}
	var maxID uint32
	for _, w := range b.Worn {
		if w.Slot == WornCreatureSlot && w.CreatureID != 0 {
			used[w.CreatureID] = true
			if w.CreatureID > maxID {
				maxID = w.CreatureID
			}
		}
	}
	for _, c := range b.Creatures {
		if c.CreatureID != 0 {
			used[c.CreatureID] = true
			if c.CreatureID > maxID {
				maxID = c.CreatureID
			}
		}
	}
	seen := map[uint32]bool{}
	alloc := func() uint32 {
		maxID++
		for maxID == 0 || used[maxID] || seen[maxID] {
			maxID++
		}
		used[maxID] = true
		seen[maxID] = true
		return maxID
	}
	for idx := range b.Worn {
		if b.Worn[idx].Slot != WornCreatureSlot {
			continue
		}
		id := b.Worn[idx].CreatureID
		if id == 0 || seen[id] {
			b.Worn[idx].CreatureID = alloc()
		} else {
			seen[id] = true
		}
	}
	order := make([]int, len(b.Creatures))
	for i := range b.Creatures {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return b.Creatures[order[i]].Slot < b.Creatures[order[j]].Slot
	})
	for _, idx := range order {
		id := b.Creatures[idx].CreatureID
		if id == 0 || seen[id] {
			b.Creatures[idx].CreatureID = alloc()
		} else {
			seen[id] = true
		}
	}
}

func (b Bag) nextCreatureID() uint32 {
	var maxID uint32
	for _, w := range b.Worn {
		if w.Slot == WornCreatureSlot && w.CreatureID > maxID {
			maxID = w.CreatureID
		}
	}
	for _, c := range b.Creatures {
		if c.CreatureID > maxID {
			maxID = c.CreatureID
		}
	}
	return maxID + 1
}

// CreatureInfoEntries collects NOTI 105 metadata entries for every creature
// owned by the character (both Space 3 worn slot 26 and Space 7 bag 0..159).
func (b Bag) CreatureInfoEntries() []protocol.CreatureInfoEntry {
	seen := map[uint32]bool{}
	var out []protocol.CreatureInfoEntry
	add := func(id uint32) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, protocol.CreatureInfoEntry{
			InstanceID: id,
			Stamina:    100,
			Level:      1,
		})
	}
	for _, w := range b.Worn {
		if w.Slot == WornCreatureSlot {
			add(w.CreatureID)
		}
	}
	for _, c := range b.Creatures {
		add(c.CreatureID)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}
func SaveBag(state json.RawMessage, b Bag) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(state, &fields); e != nil {
		return nil, e
	}
	if fields == nil {
		return nil, fmt.Errorf("character state is not an object")
	}
	v, e := json.Marshal(b)
	if e != nil {
		return nil, e
	}
	fields["inventory"] = v
	return json.Marshal(fields)
}
func (b Bag) Rows() [][protocol.CurrentItemRecordSize]byte {
	items := append([]BagItem(nil), b.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].Slot < items[j].Slot })
	rows := [][protocol.CurrentItemRecordSize]byte{protocol.OrdinaryItem(0, 0, b.Gold)}
	for _, i := range items {
		row := protocol.OrdinaryItem(i.Slot, i.Template, i.Amount)
		if i.SortLocked {
			row[protocol.ItemSortLockOffset] = 1
		}
		rows = append(rows, row)
	}
	for _, i := range b.Equipment {
		rows = append(rows, EquipmentRow(i))
	}
	sort.Slice(rows, func(i, j int) bool {
		return uint16(rows[i][0])|uint16(rows[i][1])<<8 < uint16(rows[j][0])|uint16(rows[j][1])<<8
	})
	return rows
}

// LockedItemEntries collects active item safety locks (Alt+Left-Click) across
// ordinary (space 0), avatar (space 1), and worn (space 3) slots for NOTI 251.
func (b Bag) LockedItemEntries(now int64) []protocol.ItemLockEntry {
	if now <= 0 {
		now = time.Now().Unix()
	}
	var out []protocol.ItemLockEntry
	add := func(space byte, slot uint16, state byte, unlockAt int64) {
		if state == 0 {
			return
		}
		var rem uint32
		if state == 2 {
			if unlockAt <= now {
				return
			}
			diff := unlockAt - now
			if diff > math.MaxUint32 {
				diff = math.MaxUint32
			}
			rem = uint32(diff)
		}
		out = append(out, protocol.ItemLockEntry{
			Space:            space,
			Slot:             slot,
			State:            state,
			RemainingSeconds: rem,
		})
	}
	for _, it := range b.Items {
		add(0, it.Slot, it.ItemLock, it.UnlockAt)
	}
	for _, eq := range b.Equipment {
		add(0, eq.Slot, eq.ItemLock, eq.UnlockAt)
	}
	for _, av := range b.Avatars {
		add(1, av.Slot, av.ItemLock, av.UnlockAt)
	}
	for _, eq := range b.Worn {
		add(3, eq.Slot, eq.ItemLock, eq.UnlockAt)
	}
	for _, av := range b.WornAvatars {
		add(3, av.Slot, av.ItemLock, av.UnlockAt)
	}
	for _, cr := range b.Creatures {
		add(protocol.CreatureSpace, cr.Slot, cr.ItemLock, cr.UnlockAt)
	}
	for _, ca := range b.CreatureArtifacts {
		add(protocol.CreatureSpace, ca.Slot, ca.ItemLock, ca.UnlockAt)
	}
	for _, ci := range b.CreatureItems {
		add(protocol.CreatureSpace, ci.Slot, ci.ItemLock, ci.UnlockAt)
	}
	for _, ins := range b.Insignias {
		add(protocol.InsigniaSpace, ins.Slot, ins.ItemLock, ins.UnlockAt)
	}
	for _, gm := range b.Gems {
		add(protocol.InsigniaSpace, gm.Slot, gm.ItemLock, gm.UnlockAt)
	}
	for _, tl := range b.Talismans {
		add(protocol.TalismanSpace, tl.Slot, tl.ItemLock, tl.UnlockAt)
	}
	for _, rn := range b.Runes {
		add(protocol.TalismanSpace, rn.Slot, rn.ItemLock, rn.UnlockAt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Space != out[j].Space {
			return out[i].Space < out[j].Space
		}
		return out[i].Slot < out[j].Slot
	})
	return out
}

// CreatureRows projects the character's Creature Inventory (space 7) sorted by slot:
// creatures/eggs in 0..159, artifacts in 160..215, creature consumables in 216..271.
func (b Bag) CreatureRows() [][protocol.CurrentItemRecordSize]byte {
	rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(b.Creatures)+len(b.CreatureArtifacts)+len(b.CreatureItems))
	for _, c := range b.Creatures {
		rows = append(rows, EquipmentRow(c))
	}
	for _, a := range b.CreatureArtifacts {
		rows = append(rows, EquipmentRow(a))
	}
	for _, it := range b.CreatureItems {
		row := protocol.OrdinaryItem(it.Slot, it.Template, it.Amount)
		if it.SortLocked {
			row[protocol.ItemSortLockOffset] = 1
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return uint16(rows[i][0])|uint16(rows[i][1])<<8 < uint16(rows[j][0])|uint16(rows[j][1])<<8
	})
	return rows
}

// AddCreatures places one or more creature/egg templates into free slots in space 7 (0..159).
func (b Bag) AddCreatures(ids []uint32) (Bag, []uint16, error) {
	if len(ids) == 0 {
		return b, nil, fmt.Errorf("at least one creature template is required")
	}
	occupied := map[uint16]bool{}
	for _, c := range b.Creatures {
		occupied[c.Slot] = true
	}
	slots := make([]uint16, 0, len(ids))
	for n := CreatureSlotStart; n <= CreatureSlotEnd && len(slots) < len(ids); n++ {
		if !occupied[n] {
			slots = append(slots, n)
		}
	}
	if len(slots) < len(ids) {
		return b, nil, fmt.Errorf("creature bag is full")
	}
	b.Creatures = append([]BagEquipment(nil), b.Creatures...)
	nextID := b.nextCreatureID()
	for i, id := range ids {
		if id == 0 {
			return b, nil, fmt.Errorf("invalid creature template 0")
		}
		b.Creatures = append(b.Creatures, BagEquipment{Slot: slots[i], Template: id, CreatureID: nextID})
		nextID++
	}
	return b, slots, nil
}

// AddCreatureArtifacts places one or more creature artifact templates into free slots in space 7 (160..215).
func (b Bag) AddCreatureArtifacts(ids []uint32) (Bag, []uint16, error) {
	if len(ids) == 0 {
		return b, nil, fmt.Errorf("at least one creature artifact template is required")
	}
	occupied := map[uint16]bool{}
	for _, a := range b.CreatureArtifacts {
		occupied[a.Slot] = true
	}
	slots := make([]uint16, 0, len(ids))
	for n := ArtifactSlotStart; n <= ArtifactSlotEnd && len(slots) < len(ids); n++ {
		if !occupied[n] {
			slots = append(slots, n)
		}
	}
	if len(slots) < len(ids) {
		return b, nil, fmt.Errorf("creature artifact bag is full")
	}
	b.CreatureArtifacts = append([]BagEquipment(nil), b.CreatureArtifacts...)
	for i, id := range ids {
		if id == 0 {
			return b, nil, fmt.Errorf("invalid creature artifact template 0")
		}
		b.CreatureArtifacts = append(b.CreatureArtifacts, BagEquipment{Slot: slots[i], Template: id})
	}
	return b, slots, nil
}

// AddCreatureItems places creature consumables into space 7 (216..271), merging into existing stacks up to 1000.
func (b Bag) AddCreatureItems(id, amount uint32) (Bag, uint16, error) {
	if id == 0 || amount == 0 {
		return b, 0, fmt.Errorf("invalid creature consumable award")
	}
	limit := uint32(1000)
	if amount > limit {
		limit = amount
	}
	b.CreatureItems = append([]BagItem(nil), b.CreatureItems...)
	occupied := map[uint16]bool{}
	for i, it := range b.CreatureItems {
		occupied[it.Slot] = true
		if it.Template == id && it.Slot >= CreatureItemSlotStart && it.Slot <= CreatureItemSlotEnd && uint64(it.Amount)+uint64(amount) <= uint64(limit) {
			b.CreatureItems[i].Amount += amount
			return b, it.Slot, nil
		}
	}
	for slot := CreatureItemSlotStart; slot <= CreatureItemSlotEnd; slot++ {
		if !occupied[slot] {
			b.CreatureItems = append(b.CreatureItems, BagItem{Slot: slot, Template: id, Amount: amount})
			return b, slot, nil
		}
	}
	return b, 0, fmt.Errorf("creature consumable bag is full")
}

// InsigniaRows projects the character's Insignia & Gem Inventory (space 0x25) sorted by slot:
// insignias in 0..55, gems in 56..111.
func (b Bag) InsigniaRows() [][protocol.CurrentItemRecordSize]byte {
	rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(b.Insignias)+len(b.Gems))
	for _, ins := range b.Insignias {
		rows = append(rows, EquipmentRow(ins))
	}
	for _, g := range b.Gems {
		row := protocol.OrdinaryItem(g.Slot, g.Template, g.Amount)
		if g.SortLocked {
			row[protocol.ItemSortLockOffset] = 1
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return uint16(rows[i][0])|uint16(rows[i][1])<<8 < uint16(rows[j][0])|uint16(rows[j][1])<<8
	})
	return rows
}

// AddInsignias places one or more insignia ([flag]) templates into free slots in space 0x25 (0..55).
func (b Bag) AddInsignias(ids []uint32) (Bag, []uint16, error) {
	if len(ids) == 0 {
		return b, nil, fmt.Errorf("at least one insignia template is required")
	}
	occupied := map[uint16]bool{}
	for _, ins := range b.Insignias {
		occupied[ins.Slot] = true
	}
	slots := make([]uint16, 0, len(ids))
	for n := InsigniaSlotStart; n <= InsigniaSlotEnd && len(slots) < len(ids); n++ {
		if !occupied[n] {
			slots = append(slots, n)
		}
	}
	if len(slots) < len(ids) {
		return b, nil, fmt.Errorf("insignia bag is full")
	}
	b.Insignias = append([]BagEquipment(nil), b.Insignias...)
	for i, id := range ids {
		if id == 0 {
			return b, nil, fmt.Errorf("invalid insignia template 0")
		}
		b.Insignias = append(b.Insignias, BagEquipment{Slot: slots[i], Template: id})
	}
	return b, slots, nil
}

// AddGems places one or more flag gem ([flag gem]) items into free slots in space 0x25 (56..111).
func (b Bag) AddGems(id, amount uint32) (Bag, uint16, error) {
	if id == 0 || amount == 0 {
		return b, 0, fmt.Errorf("invalid flag gem award")
	}
	limit := uint32(1000)
	if amount > limit {
		limit = amount
	}
	b.Gems = append([]BagItem(nil), b.Gems...)
	occupied := map[uint16]bool{}
	for i, g := range b.Gems {
		occupied[g.Slot] = true
		if g.Template == id && g.Slot >= GemSlotStart && g.Slot <= GemSlotEnd && uint64(g.Amount)+uint64(amount) <= uint64(limit) {
			b.Gems[i].Amount += amount
			return b, g.Slot, nil
		}
	}
	for slot := GemSlotStart; slot <= GemSlotEnd; slot++ {
		if !occupied[slot] {
			b.Gems = append(b.Gems, BagItem{Slot: slot, Template: id, Amount: amount})
			return b, slot, nil
		}
	}
	return b, 0, fmt.Errorf("flag gem bag is full")
}

// TalismanRows projects the character's Talisman & Rune Inventory (space 0x2b)
// sorted by slot: talismans in 0..79 and runes in 80..328.
func (b Bag) TalismanRows() [][protocol.CurrentItemRecordSize]byte {
	rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(b.Talismans)+len(b.Runes))
	for _, t := range b.Talismans {
		rows = append(rows, EquipmentRow(t))
	}
	for _, r := range b.Runes {
		row := protocol.OrdinaryItem(r.Slot, r.Template, r.Amount)
		if r.SortLocked {
			row[protocol.ItemSortLockOffset] = 1
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return uint16(rows[i][0])|uint16(rows[i][1])<<8 < uint16(rows[j][0])|uint16(rows[j][1])<<8
	})
	return rows
}

// AddTalismans places one or more talisman templates into free slots in space
// 0x2b (slots 0..79). Refuses atomically if the talisman tab is full.
func (b Bag) AddTalismans(ids []uint32) (Bag, []uint16, error) {
	if len(ids) == 0 {
		return b, nil, fmt.Errorf("at least one talisman template is required")
	}
	occupied := map[uint16]bool{}
	for _, t := range b.Talismans {
		occupied[t.Slot] = true
	}
	slots := make([]uint16, 0, len(ids))
	for n := uint16(0); n < TalismanSlotCount && len(slots) < len(ids); n++ {
		if !occupied[n] {
			slots = append(slots, n)
		}
	}
	if len(slots) < len(ids) {
		return b, nil, fmt.Errorf("talisman bag is full")
	}
	b.Talismans = append([]BagEquipment(nil), b.Talismans...)
	for i, id := range ids {
		if id == 0 {
			return b, nil, fmt.Errorf("invalid talisman template 0")
		}
		b.Talismans = append(b.Talismans, BagEquipment{
			Slot:        slots[i],
			Template:    id,
			Durability:  0,
			RuneSockets: DefaultTalismanRuneSockets,
		})
	}
	return b, slots, nil
}

// AddRunes places count copies of a rune template into free slots in space 0x2b
// (slots 80..319). Each rune occupies one slot with Amount=1.
func (b Bag) AddRunes(id, count uint32) (Bag, []uint16, error) {
	if id == 0 || count == 0 || count > uint32(RuneSlotEnd-RuneSlotStart+1) {
		return b, nil, fmt.Errorf("invalid rune award")
	}
	occupied := map[uint16]bool{}
	for _, r := range b.Runes {
		occupied[r.Slot] = true
	}
	slots := make([]uint16, 0, count)
	for n := RuneSlotStart; n <= RuneSlotEnd && len(slots) < int(count); n++ {
		if !occupied[n] {
			slots = append(slots, n)
		}
	}
	if len(slots) < int(count) {
		return b, nil, fmt.Errorf("rune bag is full")
	}
	b.Runes = append([]BagItem(nil), b.Runes...)
	for _, slot := range slots {
		b.Runes = append(b.Runes, BagItem{Slot: slot, Template: id, Amount: 1})
	}
	return b, slots, nil
}

// AvatarRows projects the character's Avatar Inventory (space 1) sorted by slot.
func (b Bag) AvatarRows() [][protocol.CurrentItemRecordSize]byte {
	avatars := append([]BagEquipment(nil), b.Avatars...)
	sort.Slice(avatars, func(i, j int) bool { return avatars[i].Slot < avatars[j].Slot })
	rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(avatars))
	for _, a := range avatars {
		rows = append(rows, EquipmentRow(a))
	}
	return rows
}

// AddAvatars places one or more avatar templates into free slots in space 1
// (slots 0..AvatarBagCapacity-1). Refuses atomically if the avatar bag is full.
func (b Bag) AddAvatars(ids []uint32) (Bag, []uint16, error) {
	if len(ids) == 0 {
		return b, nil, fmt.Errorf("at least one avatar template is required")
	}
	occupied := map[uint16]bool{}
	for _, a := range b.Avatars {
		occupied[a.Slot] = true
	}
	slots := make([]uint16, 0, len(ids))
	for n := uint16(0); n < AvatarBagCapacity && len(slots) < len(ids); n++ {
		if !occupied[n] {
			slots = append(slots, n)
		}
	}
	if len(slots) < len(ids) {
		return b, nil, fmt.Errorf("avatar bag is full")
	}
	b.Avatars = append([]BagEquipment(nil), b.Avatars...)
	for i, id := range ids {
		if id == 0 {
			return b, nil, fmt.Errorf("invalid avatar template 0")
		}
		b.Avatars = append(b.Avatars, BagEquipment{Slot: slots[i], Template: id, Durability: 0})
	}
	return b, slots, nil
}

// Add updates the whole bag in the caller's character transaction. It does
// not silently spill, drop or partially grant a stack when the bag is full.
func (b Bag) Add(c catalog.LootCatalog, r BagRules, id, amount uint32) (Bag, uint16, error) {
	if amount == 0 || r.Source != c.Source.Checksum {
		return b, 0, fmt.Errorf("invalid inventory award/source")
	}
	b.Items = append([]BagItem(nil), b.Items...)
	if id == 0 {
		if uint64(b.Gold)+uint64(amount) > math.MaxUint32 {
			return b, 0, fmt.Errorf("gold overflow")
		}
		b.Gold += amount
		return b, 0, nil
	}
	item, ok := c.Items[id]
	if !ok || item.Kind != "stackable" {
		return b, 0, fmt.Errorf("unsupported source item")
	}
	category := item.StackableType
	if IsEmblemTemplate(id) && r.Slots[EmblemBagCategory][0] != 0 {
		category = EmblemBagCategory
	}
	if target, ok := r.SlotAliases[category]; ok {
		category = target
	}
	slots, ok := r.Slots[category]
	if !ok {
		return b, 0, fmt.Errorf("unmapped source stackable type")
	}
	limit := item.StackLimit
	if limit == 0 {
		limit = r.MissingStackLimit
	}
	if amount > limit {
		return b, 0, fmt.Errorf("award exceeds stack limit")
	}
	occupied := map[uint16]bool{}
	for _, i := range b.Equipment {
		occupied[i.Slot] = true
	}
	for i, row := range b.Items {
		occupied[row.Slot] = true
		if row.Template == id && row.Slot >= slots[0] && row.Slot <= slots[1] && uint64(row.Amount)+uint64(amount) <= uint64(limit) {
			b.Items[i].Amount += amount
			return b, row.Slot, nil
		}
	}
	for n := uint32(slots[0]); n <= uint32(slots[1]); n++ {
		slot := uint16(n)
		if !occupied[slot] {
			b.Items = append(b.Items, BagItem{Slot: slot, Template: id, Amount: amount})
			return b, slot, nil
		}
	}
	return b, 0, fmt.Errorf("bag category is full")
}
