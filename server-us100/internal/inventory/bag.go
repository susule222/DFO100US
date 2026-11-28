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
	Version       string         `json:"version"`
	Gold          uint32         `json:"gold"`
	ExpandedSlots uint16         `json:"expanded_slots,omitempty"`
	Items         []BagItem      `json:"items"`
	Equipment     []BagEquipment `json:"equipment,omitempty"`
	Worn          []BagEquipment `json:"worn,omitempty"`
	Avatars       []BagEquipment `json:"avatars,omitempty"`
	WornAvatars   []BagEquipment `json:"worn_avatars,omitempty"`
	Talismans     []BagEquipment `json:"talismans,omitempty"`
	Runes         []BagItem      `json:"runes,omitempty"`
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
		validTalisman := i.Slot >= WornTalismanSlotMin && i.Slot <= WornTalismanSlotMax
		if (!validOrdinary && !validTalisman) || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved worn equipment")
		}
		if validTalisman && b.Worn[idx].RuneSockets == [3]byte{} {
			b.Worn[idx].RuneSockets = DefaultTalismanRuneSockets
		}
		seen[i.Slot] = true
	}
	seen = map[uint16]bool{}
	for _, i := range b.Avatars {
		if i.Slot >= AvatarBagCapacity || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved avatar item")
		}
		seen[i.Slot] = true
	}
	seen = map[uint16]bool{}
	for _, i := range b.WornAvatars {
		if i.Slot > 11 || i.Template == 0 || seen[i.Slot] {
			return b, fmt.Errorf("invalid saved worn avatar")
		}
		seen[i.Slot] = true
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
	// Auto-migrate any talismans previously placed in the ordinary Equipment tab
	// (space 0) into free slots in the Talisman tab (space 0x2b, slots 0..79).
	if len(b.Equipment) > 0 {
		var keptEquip []BagEquipment
		var migrated []BagEquipment
		for _, eq := range b.Equipment {
			if IsTalismanTemplate(eq.Template) {
				migrated = append(migrated, eq)
			} else {
				keptEquip = append(keptEquip, eq)
			}
		}
		if len(migrated) > 0 {
			sort.Slice(migrated, func(i, j int) bool { return migrated[i].Slot < migrated[j].Slot })
			for _, eq := range migrated {
				placed := false
				for slot := uint16(0); slot < TalismanSlotCount; slot++ {
					if !seen[slot] {
						seen[slot] = true
						eq.Slot = slot
						eq.Durability = 0
						if eq.RuneSockets == [3]byte{} {
							eq.RuneSockets = DefaultTalismanRuneSockets
						}
						b.Talismans = append(b.Talismans, eq)
						placed = true
						break
					}
				}
				if !placed {
					keptEquip = append(keptEquip, eq)
				}
			}
			b.Equipment = keptEquip
		}
	}
	return b, nil
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
