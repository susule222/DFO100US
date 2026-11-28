package inventory

import (
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/game/protocol"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type EquipmentDefinition struct {
	RewardOnly   bool `json:"reward_only,omitempty"`
	ID           uint32
	Path, SHA256 string
	Fields       map[string][]pvf.Token
}
type EquipmentCatalog struct {
	Source     pvf.ArchiveSnapshot   `json:"source"`
	Rows       []EquipmentDefinition `json:"rows"`
	index      map[uint32]EquipmentDefinition
	pool       []EquipmentDrop
	rewardPool []RewardDrop
}

// EquipmentDrop is one piece of gear this build can actually place in a bag,
// with the source fields a drop roll selects on.
type EquipmentDrop struct {
	ID            uint32
	Grade, Rarity int32
	Durability    uint16
}

// RewardDrop is any bag-usable combat equipment (all rarities including Epic
// and Mythic) available to global difficulty drops and abyss drop pools.
type RewardDrop struct {
	ID         uint32
	Grade      int32
	MinLevel   int32
	Rarity     int32
	Durability uint16
	Kind       string
}

// DropPool is the catalog's bag-usable gear, projected once at load. Basic
// decides membership, so anything the pool offers can always be granted.
func (c *EquipmentCatalog) DropPool() []EquipmentDrop {
	if c == nil {
		return nil
	}
	return c.pool
}

// RewardPool returns all bag-usable non-avatar combat equipment across every
// rarity (Common through Mythic), ordered deterministically for drop rolls.
func (c *EquipmentCatalog) RewardPool() []RewardDrop {
	if c == nil {
		return nil
	}
	return c.rewardPool
}

func (c *EquipmentCatalog) Durability(id uint32) (uint16, bool) {
	if c == nil {
		return 0, false
	}
	for _, d := range c.pool {
		if d.ID == id {
			return d.Durability, true
		}
	}
	return 0, false
}

func LoadEquipmentCatalog(path, source string) (*EquipmentCatalog, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var c EquipmentCatalog
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	if c.Source.Checksum != source || len(c.Rows) == 0 {
		return nil, fmt.Errorf("equipment source mismatch")
	}
	c.index = map[uint32]EquipmentDefinition{}
	for _, r := range c.Rows {
		if r.ID == 0 || len(r.SHA256) != 64 || r.Path == "" {
			return nil, fmt.Errorf("invalid equipment source")
		}
		c.index[r.ID] = r
	}
	// Project the drop pool once. Basic is the same acceptance test the award
	// path uses, so nothing enters the pool that could later fail to be
	// granted; gear it rejects stays undroppable rather than being guessed at.
	combatKinds := map[string]bool{
		"[weapon]": true, "[coat]": true, "[pants]": true, "[shoulder]": true,
		"[waist]": true, "[shoes]": true, "[wrist]": true, "[amulet]": true,
		"[ring]": true, "[support]": true, "[magic stone]": true, "[earring]": true,
	}
	for _, r := range c.Rows {
		if rd, err := c.Reward(r.ID); err == nil {
			kindTok := ""
			if k := r.Fields["[equipment type]"]; len(k) > 0 {
				kindTok = k[0].Text
			}
			rarityTok := r.Fields["[rarity]"]
			if combatKinds[kindTok] && len(rarityTok) == 1 && rarityTok[0].Type == 0 && rarityTok[0].Value >= 0 {
				var grd, mlv int32
				if g := r.Fields["[grade]"]; len(g) == 1 && g[0].Type == 0 && g[0].Value > 0 {
					grd = g[0].Value
				}
				if m := r.Fields["[minimum level]"]; len(m) == 1 && m[0].Type == 0 && m[0].Value > 0 {
					mlv = m[0].Value
				}
				if mlv == 0 {
					mlv = grd
				}
				if grd == 0 {
					grd = mlv
				}
				c.rewardPool = append(c.rewardPool, RewardDrop{
					ID: r.ID, Grade: grd, MinLevel: mlv, Rarity: rarityTok[0].Value,
					Durability: rd, Kind: kindTok,
				})
			}
		}
		durability, err := c.Basic(r.ID)
		if err != nil || r.RewardOnly {
			continue
		}
		grade, rarity := r.Fields["[grade]"], r.Fields["[rarity]"]
		if len(grade) != 1 || grade[0].Type != 0 || grade[0].Value <= 0 {
			continue
		}
		if len(rarity) != 1 || rarity[0].Type != 0 || rarity[0].Value < 0 {
			continue
		}
		c.pool = append(c.pool, EquipmentDrop{r.ID, grade[0].Value, rarity[0].Value, durability})
	}
	sort.Slice(c.pool, func(i, j int) bool {
		if c.pool[i].Grade != c.pool[j].Grade {
			return c.pool[i].Grade < c.pool[j].Grade
		}
		return c.pool[i].ID < c.pool[j].ID
	})
	sort.Slice(c.rewardPool, func(i, j int) bool {
		if c.rewardPool[i].Rarity != c.rewardPool[j].Rarity {
			return c.rewardPool[i].Rarity < c.rewardPool[j].Rarity
		}
		if c.rewardPool[i].MinLevel != c.rewardPool[j].MinLevel {
			return c.rewardPool[i].MinLevel < c.rewardPool[j].MinLevel
		}
		return c.rewardPool[i].ID < c.rewardPool[j].ID
	})
	if err := c.includeNeoRentals(); err != nil {
		return nil, err
	}
	return &c, nil
}

type BagEquipment struct {
	RentalUntil  int64   `json:"neo_rental_until,omitempty"`
	Slot         uint16  `json:"slot"`
	Template     uint32  `json:"template"`
	Durability   uint16  `json:"durability"`
	CreatureID   uint32  `json:"creature_id,omitempty"`
	Upgrade      byte    `json:"upgrade,omitempty"`
	AmplifyType  byte    `json:"amplify_type,omitempty"`
	AmplifyValue uint16  `json:"amplify_value,omitempty"`
	RuneSockets  [3]byte `json:"rune_sockets,omitempty"`
	SortLocked   bool    `json:"sort_locked,omitempty"`
	ItemLock     byte    `json:"item_lock,omitempty"`
	UnlockAt     int64   `json:"unlock_at,omitempty"`
}

// Has reports whether equipment template id exists in the loaded equipment catalog.
func (c *EquipmentCatalog) Has(id uint32) bool {
	if c == nil || id == 0 {
		return false
	}
	_, ok := c.index[id]
	return ok
}

// IsTalismanTemplate reports whether an equipment template ID belongs to the
// US100 PVF talisman namespace (all 1,513 entries in list/equipment.lst under
// equipment/character/common/talisman/*.equ live exclusively in [100400000..100401470]
// and [400400000..400400071] with zero non-talisman overlap).
func IsTalismanTemplate(id uint32) bool {
	return (id >= 100400000 && id <= 100401470) || (id >= 400400000 && id <= 400400071)
}

// IsTalisman reports whether template id is a talisman according to either the
// loaded equipment catalog ([equipment type] == [talisman]) or the US100 PVF
// talisman template bands.
func (c *EquipmentCatalog) IsTalisman(id uint32) bool {
	if c != nil {
		if def, ok := c.index[id]; ok {
			if k := def.Fields["[equipment type]"]; len(k) > 0 && k[0].Text == "[talisman]" {
				return true
			}
			return false
		}
	}
	return IsTalismanTemplate(id)
}

// EffectiveUpgrade returns the displayed reinforcement/amplification level.
// PC rental equipment defaults to +7 when no explicit upgrade level is saved.
func (i BagEquipment) EffectiveUpgrade() byte {
	if i.Upgrade > 0 {
		return i.Upgrade
	}
	if i.RentalUntil > 0 {
		return 7
	}
	return 0
}

// Meta returns (minLevel, rarity, ok) for an equipment template from the catalog.
func (c *EquipmentCatalog) Meta(id uint32) (minLevel, rarity int32, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	r, found := c.index[id]
	if !found {
		return 0, 0, false
	}
	if m := r.Fields["[minimum level]"]; len(m) == 1 && m[0].Type == 0 && m[0].Value > 0 {
		minLevel = m[0].Value
	}
	if minLevel == 0 {
		if g := r.Fields["[grade]"]; len(g) == 1 && g[0].Type == 0 && g[0].Value > 0 {
			minLevel = g[0].Value
		}
	}
	if rr := r.Fields["[rarity]"]; len(rr) == 1 && rr[0].Type == 0 && rr[0].Value >= 0 {
		rarity = rr[0].Value
	}
	return minLevel, rarity, true
}

// Reward accepts the gear a quest or an operator hands out. It keeps every
// structural check Basic makes - exactly one attach-type cell, a readable
// rarity, a known equipment kind, a usable durability - but not Basic's two
// drop-pool rules: a reward may be account- or character-bound, and it may be
// rarer than rare. Live capture 20260912T011904 shows quest 21650 refused four
// times as "special equipment reward requires additional source state" after
// its template was imported, because 100261068 is bound gear; the reward is
// ordinary, it simply is not pool gear.
func (c *EquipmentCatalog) Reward(id uint32) (uint16, error) {
	r, ok := c.index[id]
	if !ok {
		return 0, fmt.Errorf("equipment absent from source")
	}
	attach, rarity, kind := r.Fields["[attach type]"], r.Fields["[rarity]"], r.Fields["[equipment type]"]
	if len(attach) != 1 || len(rarity) != 1 || rarity[0].Type != 0 || rarity[0].Value < 0 || len(kind) == 0 {
		return 0, fmt.Errorf("special equipment reward requires additional source state")
	}
	d := r.Fields["[durability]"]
	if len(d) == 0 {
		switch kind[0].Text {
		case "[amulet]", "[wrist]", "[ring]", "[support]", "[magic stone]", "[earring]", "[title name]", "[talisman]":
			return 0, nil
		}
		return 0, fmt.Errorf("missing source equipment durability")
	}
	if len(d) != 1 || d[0].Type != 0 || d[0].Value < 0 || d[0].Value > 65535 {
		return 0, fmt.Errorf("invalid equipment durability")
	}
	return uint16(d[0].Value), nil
}

// Basic is Reward plus the two rules that decide what a monster may drop:
// the piece has to be unbound, and no rarer than rare. The drop pool is built
// from this, so anything a drop offers is also grantable.
func (c *EquipmentCatalog) Basic(id uint32) (uint16, error) {
	d, e := c.Reward(id)
	if e != nil {
		return 0, e
	}
	r := c.index[id]
	attach, rarity := r.Fields["[attach type]"], r.Fields["[rarity]"]
	if attach[0].Text != "[free]" || rarity[0].Value > 1 {
		return 0, fmt.Errorf("special equipment reward requires additional source state")
	}
	return d, nil
}
func EquipmentRow(i BagEquipment) [protocol.CurrentItemRecordSize]byte {
	// Current NOTI13 logs (0x1434fc780..0x1434fc966) verify exact US224 row offsets:
	//   +0x00 u16 slot, +0x02 u32 template, +0x06 u32 data (creature instance ID for creatures),
	//   +0x0a u8 upgrade, +0x0b u16 durability, +0x0d u8 isSealed, +0x0e u32 enchantCard,
	//   +0x12 u8 enchantUpgrade, +0x13 u8 amplify_type, +0x14 u16 amplify_value,
	//   +0x55 u8 sortLocked, +0x67..0x69 [3]u8 talisman rune socket colors (1..5).
	r := protocol.OrdinaryItem(i.Slot, i.Template, i.CreatureID)
	r[10] = i.EffectiveUpgrade()
	binary.LittleEndian.PutUint16(r[11:], i.Durability)
	if i.AmplifyType != 0 {
		r[19] = i.AmplifyType
		binary.LittleEndian.PutUint16(r[20:], i.AmplifyValue)
	}
	if i.RuneSockets != [3]byte{} {
		r[protocol.TalismanRuneSocketsOffset] = i.RuneSockets[0]
		r[protocol.TalismanRuneSocketsOffset+1] = i.RuneSockets[1]
		r[protocol.TalismanRuneSocketsOffset+2] = i.RuneSockets[2]
	}
	if i.SortLocked {
		r[protocol.ItemSortLockOffset] = 1
	}
	return r
}
func (b Bag) AddEquipment(c *EquipmentCatalog, slots [2]uint16, id, count uint32) (Bag, []uint16, error) {
	if c != nil && c.IsTalisman(id) {
		if count == 0 || count > uint32(TalismanSlotCount) {
			return b, nil, fmt.Errorf("invalid talisman award")
		}
		ids := make([]uint32, count)
		for i := range ids {
			ids[i] = id
		}
		return b.AddTalismans(ids)
	}
	if c == nil || slots[0] == 0 || slots[0] > slots[1] || count == 0 || count > uint32(slots[1]-slots[0]+1) {
		return b, nil, fmt.Errorf("invalid equipment award")
	}
	// Granting uses the reward rule, not the drop-pool rule: a quest reward is
	// routinely bound and may be above rare. Drops are unaffected, because the
	// pool they choose from is built from Basic.
	d, e := c.Reward(id)
	if e != nil {
		return b, nil, e
	}
	occupied := map[uint16]bool{}
	for _, i := range b.Items {
		occupied[i.Slot] = true
	}
	for _, i := range b.Equipment {
		occupied[i.Slot] = true
	}
	var available []uint16
	for n := uint32(slots[0]); n <= uint32(slots[1]) && len(available) < int(count); n++ {
		if !occupied[uint16(n)] {
			available = append(available, uint16(n))
		}
	}
	if len(available) != int(count) {
		return b, nil, fmt.Errorf("equipment bag is full")
	}
	b.Equipment = append([]BagEquipment(nil), b.Equipment...)
	for _, n := range available {
		b.Equipment = append(b.Equipment, BagEquipment{Slot: n, Template: id, Durability: d})
	}
	return b, available, nil
}

// AddRawEquipment places count copies of equipment template id (with source
// PVF durability) into free slots in the equipment tab (slots[0]..slots[1]).
// Talismans are automatically routed to the Talisman tab (space 0x2b, 0..79).
// Used by PVF-backed booster/selection/package boxes whose reward items may
// not be in the monster-drop equipment.generated.json pool (e.g. Lv.100 event
// weapons, epics, titles, talismans).
func (b Bag) AddRawEquipment(slots [2]uint16, id uint32, durability uint16, count uint32) (Bag, []uint16, error) {
	if IsTalismanTemplate(id) {
		if count == 0 || count > uint32(TalismanSlotCount) {
			return b, nil, fmt.Errorf("invalid talisman award")
		}
		ids := make([]uint32, count)
		for i := range ids {
			ids[i] = id
		}
		return b.AddTalismans(ids)
	}
	if id == 0 || slots[0] == 0 || slots[0] > slots[1] || count == 0 || count > uint32(slots[1]-slots[0]+1) {
		return b, nil, fmt.Errorf("invalid equipment award")
	}
	occupied := map[uint16]bool{}
	for _, i := range b.Items {
		occupied[i.Slot] = true
	}
	for _, i := range b.Equipment {
		occupied[i.Slot] = true
	}
	var available []uint16
	for n := uint32(slots[0]); n <= uint32(slots[1]) && len(available) < int(count); n++ {
		if !occupied[uint16(n)] {
			available = append(available, uint16(n))
		}
	}
	if len(available) != int(count) {
		return b, nil, fmt.Errorf("equipment bag is full")
	}
	b.Equipment = append([]BagEquipment(nil), b.Equipment...)
	for _, n := range available {
		b.Equipment = append(b.Equipment, BagEquipment{Slot: n, Template: id, Durability: durability})
	}
	return b, available, nil
}
