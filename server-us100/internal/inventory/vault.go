package inventory

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"time"
)

type VaultRules struct {
	SourceSHA256  string   `json:"source_sha256"`
	InitialSlots  uint16   `json:"initial_slots"`
	VerifiedSlots []uint16 `json:"verified_slots"`
}

func LoadVaultRules(path string) (VaultRules, error) {
	var r VaultRules
	b, e := os.ReadFile(path)
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if len(r.SourceSHA256) != 64 || !r.allows(r.InitialSlots) {
		return r, fmt.Errorf("invalid source vault configuration")
	}
	return r, nil
}
func (r VaultRules) allows(n uint16) bool {
	for _, v := range r.VerifiedSlots {
		if n != 0 && n == v {
			return true
		}
	}
	return false
}

// PersonalVaultTiers is the 17-tier native US100 Personal Safe I / Safe II
// capacity table verified at DFO_unpacked.exe 0x140057760..0x140057e8a (8..264).
var PersonalVaultTiers = []uint16{
	8, 24, 40, 56, 72, 88, 104, 120, 136, 152, 168, 184, 200, 216, 232, 248, 264,
}

const (
	MaxPersonalVaultSlots uint16 = 264
	MaxAccountVaultSlots  uint16 = 240
)

// NextPersonalVaultSlots computes the next valid Personal Safe capacity from
// current capacity cur and optional targetSlots (0 means advance by one tier,
// +16 slots; non-zero jumps to at least targetSlots snapped to a valid tier).
func NextPersonalVaultSlots(cur, target uint16) (uint16, error) {
	if cur >= MaxPersonalVaultSlots {
		return cur, fmt.Errorf("personal vault is already at maximum capacity (%d)", MaxPersonalVaultSlots)
	}
	if target > 0 {
		for _, capSlots := range PersonalVaultTiers {
			if capSlots >= target && capSlots > cur {
				return capSlots, nil
			}
		}
		// If target is already <= cur (e.g. player bought a lower-tier kit),
		// still advance by one tier so the purchase/kit takes effect cleanly.
	}
	for _, capSlots := range PersonalVaultTiers {
		if capSlots > cur {
			return capSlots, nil
		}
	}
	return cur, fmt.Errorf("personal vault is already at maximum capacity (%d)", MaxPersonalVaultSlots)
}

// NextAccountVaultSlots computes the next valid Account Vault capacity (8..240,
// step +8, verified from US100 PVF etc/accountcargo.etc).
func NextAccountVaultSlots(cur, target uint16) (uint16, error) {
	if cur >= MaxAccountVaultSlots {
		return cur, fmt.Errorf("account vault is already at maximum capacity (%d)", MaxAccountVaultSlots)
	}
	if target > cur {
		// Snap up to nearest multiple of 8, capped at 240.
		snapped := ((target + 7) / 8) * 8
		if snapped > MaxAccountVaultSlots {
			snapped = MaxAccountVaultSlots
		}
		return snapped, nil
	}
	next := cur + 8
	if next > MaxAccountVaultSlots {
		next = MaxAccountVaultSlots
	}
	return next, nil
}

type VaultItem struct {
	Slot         uint16  `json:"slot"`
	Template     uint32  `json:"template"`
	Amount       uint32  `json:"amount,omitempty"`
	IsEquipment  bool    `json:"is_equipment,omitempty"`
	Durability   uint16  `json:"durability,omitempty"`
	Upgrade      byte    `json:"upgrade,omitempty"`
	AmplifyType  byte    `json:"amplify_type,omitempty"`
	AmplifyValue uint16  `json:"amplify_value,omitempty"`
	RuneSockets  [3]byte `json:"rune_sockets,omitempty"`
	RentalUntil  int64   `json:"neo_rental_until,omitempty"`
	SortLocked   bool    `json:"sort_locked,omitempty"`
	ItemLock     byte    `json:"item_lock,omitempty"`
	UnlockAt     int64   `json:"unlock_at,omitempty"`
}

func (v VaultItem) isEquip() bool {
	return v.IsEquipment || v.Amount == 0
}

func (v VaultItem) toBagEquipment(slot uint16) BagEquipment {
	return BagEquipment{
		RentalUntil:  v.RentalUntil,
		Slot:         slot,
		Template:     v.Template,
		Durability:   v.Durability,
		Upgrade:      v.Upgrade,
		AmplifyType:  v.AmplifyType,
		AmplifyValue: v.AmplifyValue,
		RuneSockets:  v.RuneSockets,
		SortLocked:   v.SortLocked,
		ItemLock:     v.ItemLock,
		UnlockAt:     v.UnlockAt,
	}
}

func vaultItemFromEquipment(slot uint16, eq BagEquipment) VaultItem {
	return VaultItem{
		Slot:         slot,
		Template:     eq.Template,
		IsEquipment:  true,
		Durability:   eq.Durability,
		Upgrade:      eq.Upgrade,
		AmplifyType:  eq.AmplifyType,
		AmplifyValue: eq.AmplifyValue,
		RuneSockets:  eq.RuneSockets,
		RentalUntil:  eq.RentalUntil,
		SortLocked:   eq.SortLocked,
		ItemLock:     eq.ItemLock,
		UnlockAt:     eq.UnlockAt,
	}
}

func vaultItemFromStack(slot uint16, it BagItem) VaultItem {
	return VaultItem{
		Slot:       slot,
		Template:   it.Template,
		Amount:     it.Amount,
		SortLocked: it.SortLocked,
		ItemLock:   it.ItemLock,
		UnlockAt:   it.UnlockAt,
	}
}

func (v VaultItem) toBagItem(slot uint16) BagItem {
	return BagItem{
		Slot:       slot,
		Template:   v.Template,
		Amount:     v.Amount,
		SortLocked: v.SortLocked,
		ItemLock:   v.ItemLock,
		UnlockAt:   v.UnlockAt,
	}
}

func ParseVaultItems(raw json.RawMessage) ([]VaultItem, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []VaultItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	seen := make(map[uint16]bool, len(items))
	for _, it := range items {
		if it.Template == 0 || seen[it.Slot] {
			return nil, fmt.Errorf("invalid saved vault item at slot %d", it.Slot)
		}
		seen[it.Slot] = true
	}
	return items, nil
}

func MarshalVaultItems(items []VaultItem) (json.RawMessage, error) {
	if len(items) == 0 {
		return json.RawMessage("[]"), nil
	}
	cp := append([]VaultItem(nil), items...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Slot < cp[j].Slot })
	b, err := json.Marshal(cp)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func VaultItemRow(it VaultItem) [protocol.CurrentItemRecordSize]byte {
	if it.isEquip() {
		return EquipmentRow(it.toBagEquipment(it.Slot))
	}
	row := protocol.OrdinaryItem(it.Slot, it.Template, it.Amount)
	if it.SortLocked {
		row[protocol.ItemSortLockOffset] = 1
	}
	return row
}

func VaultRows(items []VaultItem) [][protocol.CurrentItemRecordSize]byte {
	if len(items) == 0 {
		return nil
	}
	cp := append([]VaultItem(nil), items...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Slot < cp[j].Slot })
	rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(cp))
	for _, it := range cp {
		rows = append(rows, VaultItemRow(it))
	}
	return rows
}

type VaultService struct {
	Store *storage.Store
	Rules VaultRules
}

func (s *VaultService) Bootstrap(ctx context.Context, role storage.Character) ([]byte, error) {
	v1, _, _, err := s.BootstrapAll(ctx, role)
	return v1, err
}

// BootstrapAll builds NOTI 13 restore payloads for Personal Safe I (space 2),
// Personal Safe II (space 0x2d, omitted when 0), and Account Vault (space 0x0c,
// omitted when 0), including all persisted items.
func (s *VaultService) BootstrapAll(ctx context.Context, role storage.Character) (vault1, vault2, accountVault []byte, err error) {
	v, e := s.Store.LoadVault(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, s.Rules.SourceSHA256)
	if e != nil {
		return nil, nil, nil, e
	}
	if v.ConfigVersion != s.Rules.SourceSHA256 || !s.Rules.allows(v.Slots) {
		return nil, nil, nil, fmt.Errorf("vault requires configuration migration")
	}
	items1, e := ParseVaultItems(v.Items)
	if e != nil {
		return nil, nil, nil, e
	}
	vault1, err = protocol.US224PersonalVaultRestore(v.Slots, VaultRows(items1))
	if err != nil {
		return nil, nil, nil, err
	}
	if v.Slots2 > 0 {
		items2, e := ParseVaultItems(v.Items2)
		if e != nil {
			return nil, nil, nil, e
		}
		vault2, err = protocol.US224PersonalVault2Restore(v.Slots2, VaultRows(items2))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if av, aerr := s.Store.LoadAccountVault(ctx, role.AccountID); aerr == nil && av.Slots > 0 {
		accItems, e := ParseVaultItems(av.Items)
		if e != nil {
			return nil, nil, nil, e
		}
		accountVault, err = protocol.US224AccountVaultRestore(av.Slots, av.Gold, VaultRows(accItems))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return vault1, vault2, accountVault, nil
}

// AccountVaultRestorePayload encodes NOTI 13 space 0x0c with current capacity,
// gold, and all persisted shared Account Vault items.
func (s *VaultService) AccountVaultRestorePayload(ctx context.Context, account int64) ([]byte, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("vault service unavailable")
	}
	av, err := s.Store.LoadAccountVault(ctx, account)
	if err != nil {
		return nil, err
	}
	if av.Slots == 0 {
		return nil, nil
	}
	items, err := ParseVaultItems(av.Items)
	if err != nil {
		return nil, err
	}
	return protocol.US224AccountVaultRestore(av.Slots, av.Gold, VaultRows(items))
}

func bagSlotAcceptsItem(rules BagRules, slot uint16, item VaultItem) bool {
	if item.isEquip() {
		return rules.EquipmentSlots[0] > 0 && slot >= rules.EquipmentSlots[0] && slot <= rules.EquipmentSlots[1]
	}
	if rules.Quick(slot) {
		return true
	}
	for _, rng := range rules.Slots {
		if slot >= rng[0] && slot <= rng[1] {
			return true
		}
	}
	return false
}

func vaultSliceForSpace(space byte, pv storage.VaultState, av storage.AccountVaultState, items1, items2, accItems *[]VaultItem) (*[]VaultItem, uint16, error) {
	switch space {
	case protocol.PersonalVaultSpace:
		return items1, pv.Slots, nil
	case protocol.PersonalVault2Space:
		if pv.Slots2 == 0 {
			return nil, 0, fmt.Errorf("personal safe II is not unlocked")
		}
		return items2, pv.Slots2, nil
	case protocol.AccountVaultSpace:
		if av.Slots == 0 {
			return nil, 0, fmt.Errorf("account vault is not unlocked")
		}
		return accItems, av.Slots, nil
	default:
		return nil, 0, fmt.Errorf("unsupported vault space %d", space)
	}
}

func findVaultItem(items []VaultItem, slot uint16) (int, *VaultItem) {
	for i := range items {
		if items[i].Slot == slot {
			cp := items[i]
			return i, &cp
		}
	}
	return -1, nil
}

func setVaultItem(items *[]VaultItem, slot uint16, item *VaultItem) {
	kept := make([]VaultItem, 0, len(*items)+1)
	for _, v := range *items {
		if v.Slot != slot {
			kept = append(kept, v)
		}
	}
	if item != nil {
		cp := *item
		cp.Slot = slot
		kept = append(kept, cp)
	}
	*items = kept
}

func findBagSpace0Item(b Bag, slot uint16) (*VaultItem, error) {
	for _, eq := range b.Equipment {
		if eq.Slot == slot {
			v := vaultItemFromEquipment(slot, eq)
			return &v, nil
		}
	}
	for _, it := range b.Items {
		if it.Slot == slot {
			v := vaultItemFromStack(slot, it)
			return &v, nil
		}
	}
	return nil, nil
}

func setBagSpace0Item(b *Bag, slot uint16, item *VaultItem) {
	keptEq := make([]BagEquipment, 0, len(b.Equipment)+1)
	for _, eq := range b.Equipment {
		if eq.Slot != slot {
			keptEq = append(keptEq, eq)
		}
	}
	keptIt := make([]BagItem, 0, len(b.Items)+1)
	for _, it := range b.Items {
		if it.Slot != slot {
			keptIt = append(keptIt, it)
		}
	}
	if item != nil {
		if item.isEquip() {
			keptEq = append(keptEq, item.toBagEquipment(slot))
		} else {
			keptIt = append(keptIt, item.toBagItem(slot))
		}
	}
	b.Equipment = keptEq
	b.Items = keptIt
}

func space0RowAt(b Bag, slot uint16) [protocol.CurrentItemRecordSize]byte {
	for _, eq := range b.Equipment {
		if eq.Slot == slot {
			return EquipmentRow(eq)
		}
	}
	for _, it := range b.Items {
		if it.Slot == slot {
			row := protocol.OrdinaryItem(it.Slot, it.Template, it.Amount)
			if it.SortLocked {
				row[protocol.ItemSortLockOffset] = 1
			}
			return row
		}
	}
	return protocol.EmptySlotItem(slot)
}

func vaultRowAt(items []VaultItem, slot uint16) [protocol.CurrentItemRecordSize]byte {
	if _, it := findVaultItem(items, slot); it != nil {
		return VaultItemRow(*it)
	}
	return protocol.EmptySlotItem(slot)
}

// MoveItem atomically transfers, splits, merges, or swaps an item between
// Space 0 (Ordinary Bag) and any unlocked Vault (0x02 Safe I, 0x2d Safe II,
// 0x0c Account Vault), or within/between vaults.
func (s *VaultService) MoveItem(
	ctx context.Context,
	role storage.Character,
	rules BagRules,
	key string,
	r protocol.ItemMoveRequest,
) (storage.Character, uint32, [][protocol.CurrentItemRecordSize]byte, [][protocol.CurrentItemRecordSize]byte, error) {
	if s == nil || s.Store == nil {
		return role, 0, nil, nil, fmt.Errorf("vault service unavailable")
	}
	srcIsVault := protocol.IsVaultSpace(r.SourceList)
	dstIsVault := protocol.IsVaultSpace(r.DestinationList)
	if !srcIsVault && !dstIsVault {
		return role, 0, nil, nil, fmt.Errorf("move is not a vault transfer")
	}
	if (r.SourceList != 0 && !srcIsVault) || (r.DestinationList != 0 && !dstIsVault) {
		return role, 0, nil, nil, fmt.Errorf("unsupported vault transfer spaces %d->%d", r.SourceList, r.DestinationList)
	}
	if r.SourceList == r.DestinationList && r.SourceSlot == r.DestinationSlot {
		return role, 0, nil, nil, fmt.Errorf("identical vault locations")
	}
	stackLimit := rules.MissingStackLimit
	if stackLimit == 0 {
		stackLimit = 1000
	}

	var movedCount uint32
	var srcRows, dstRows [][protocol.CurrentItemRecordSize]byte

	saved, pv, av, _, err := s.Store.CommitVaultOp(
		ctx,
		role.AccountID,
		role.ID,
		s.Rules.InitialSlots,
		s.Rules.SourceSHA256,
		role.ConfigVersion,
		key,
		"US224-vault-item-move-v1",
		func(cur storage.Character, curPV storage.VaultState, curAV storage.AccountVaultState) (json.RawMessage, storage.VaultState, storage.AccountVaultState, json.RawMessage, error) {
			b, err := ReadBag(cur.State)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			items1, err := ParseVaultItems(curPV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			items2, err := ParseVaultItems(curPV.Items2)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			accItems, err := ParseVaultItems(curAV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}

			readSlot := func(space byte, slot uint16) (*VaultItem, error) {
				if space == 0 {
					return findBagSpace0Item(b, slot)
				}
				list, capSlots, err := vaultSliceForSpace(space, curPV, curAV, &items1, &items2, &accItems)
				if err != nil {
					return nil, err
				}
				if slot >= capSlots {
					return nil, fmt.Errorf("vault slot %d out of capacity %d", slot, capSlots)
				}
				_, it := findVaultItem(*list, slot)
				return it, nil
			}
			writeSlot := func(space byte, slot uint16, item *VaultItem) error {
				if space == 0 {
					if item != nil && !bagSlotAcceptsItem(rules, slot, *item) {
						return fmt.Errorf("bag slot %d does not accept template %d", slot, item.Template)
					}
					setBagSpace0Item(&b, slot, item)
					return nil
				}
				list, capSlots, err := vaultSliceForSpace(space, curPV, curAV, &items1, &items2, &accItems)
				if err != nil {
					return err
				}
				if slot >= capSlots {
					return fmt.Errorf("vault slot %d out of capacity %d", slot, capSlots)
				}
				setVaultItem(list, slot, item)
				return nil
			}

			srcItem, err := readSlot(r.SourceList, r.SourceSlot)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			dstItem, err := readSlot(r.DestinationList, r.DestinationSlot)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			if srcItem == nil {
				return nil, curPV, curAV, nil, fmt.Errorf("source slot %d:%d is empty", r.SourceList, r.SourceSlot)
			}
			if r.SourceItem != 0 && r.SourceItem != srcItem.Template {
				return nil, curPV, curAV, nil, fmt.Errorf("stale source item %d != %d", r.SourceItem, srcItem.Template)
			}
			if r.DestinationItem != 0 && (dstItem == nil || r.DestinationItem != dstItem.Template) {
				return nil, curPV, curAV, nil, fmt.Errorf("stale destination item")
			}

			if srcItem.isEquip() {
				if r.Count > 1 {
					return nil, curPV, curAV, nil, fmt.Errorf("invalid equipment move count %d", r.Count)
				}
				movedCount = 1
				if err = writeSlot(r.DestinationList, r.DestinationSlot, srcItem); err != nil {
					return nil, curPV, curAV, nil, err
				}
				if err = writeSlot(r.SourceList, r.SourceSlot, dstItem); err != nil {
					return nil, curPV, curAV, nil, err
				}
			} else {
				reqCount := r.Count
				if reqCount == 0 || reqCount > srcItem.Amount {
					reqCount = srcItem.Amount
				}
				if dstItem == nil {
					// Move full stack or partial split into empty slot.
					moved := *srcItem
					moved.Amount = reqCount
					var remain *VaultItem
					if reqCount < srcItem.Amount {
						cp := *srcItem
						cp.Amount = srcItem.Amount - reqCount
						remain = &cp
					}
					if err = writeSlot(r.DestinationList, r.DestinationSlot, &moved); err != nil {
						return nil, curPV, curAV, nil, err
					}
					if err = writeSlot(r.SourceList, r.SourceSlot, remain); err != nil {
						return nil, curPV, curAV, nil, err
					}
					movedCount = reqCount
				} else if !dstItem.isEquip() && dstItem.Template == srcItem.Template {
					// Merge same stackable template up to stack limit.
					effLimit := stackLimit
					if dstItem.Amount > effLimit {
						effLimit = dstItem.Amount
					}
					if srcItem.Amount > effLimit {
						effLimit = srcItem.Amount
					}
					spaceLeft := uint32(0)
					if effLimit > dstItem.Amount {
						spaceLeft = effLimit - dstItem.Amount
					}
					if spaceLeft == 0 {
						return nil, curPV, curAV, nil, fmt.Errorf("destination stack is full")
					}
					actual := reqCount
					if actual > spaceLeft {
						actual = spaceLeft
					}
					merged := *dstItem
					merged.Amount += actual
					var remain *VaultItem
					if actual < srcItem.Amount {
						cp := *srcItem
						cp.Amount = srcItem.Amount - actual
						remain = &cp
					}
					if err = writeSlot(r.DestinationList, r.DestinationSlot, &merged); err != nil {
						return nil, curPV, curAV, nil, err
					}
					if err = writeSlot(r.SourceList, r.SourceSlot, remain); err != nil {
						return nil, curPV, curAV, nil, err
					}
					movedCount = actual
				} else {
					// Swap full stack with different item.
					if reqCount < srcItem.Amount {
						return nil, curPV, curAV, nil, fmt.Errorf("cannot split stack onto occupied slot")
					}
					if err = writeSlot(r.DestinationList, r.DestinationSlot, srcItem); err != nil {
						return nil, curPV, curAV, nil, err
					}
					if err = writeSlot(r.SourceList, r.SourceSlot, dstItem); err != nil {
						return nil, curPV, curAV, nil, err
					}
					movedCount = srcItem.Amount
				}
			}

			nextState, err := SaveBag(cur.State, b)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			curPV.Items, err = MarshalVaultItems(items1)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			curPV.Items2, err = MarshalVaultItems(items2)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			curAV.Items, err = MarshalVaultItems(accItems)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			receipt, err := json.Marshal(map[string]any{
				"src_space": r.SourceList, "src_slot": r.SourceSlot,
				"dst_space": r.DestinationList, "dst_slot": r.DestinationSlot,
				"count": movedCount,
			})
			return nextState, curPV, curAV, receipt, err
		},
	)
	if err != nil {
		return role, 0, nil, nil, err
	}
	saved.WireID = role.WireID

	// Build authoritative NOTI 14 rows for source and destination slots from persisted state.
	bagAfter, err := ReadBag(saved.State)
	if err != nil {
		return saved, movedCount, nil, nil, err
	}
	items1, _ := ParseVaultItems(pv.Items)
	items2, _ := ParseVaultItems(pv.Items2)
	accItems, _ := ParseVaultItems(av.Items)
	rowFor := func(space byte, slot uint16) [protocol.CurrentItemRecordSize]byte {
		switch space {
		case 0:
			return space0RowAt(bagAfter, slot)
		case protocol.PersonalVaultSpace:
			return vaultRowAt(items1, slot)
		case protocol.PersonalVault2Space:
			return vaultRowAt(items2, slot)
		case protocol.AccountVaultSpace:
			return vaultRowAt(accItems, slot)
		default:
			return protocol.EmptySlotItem(slot)
		}
	}
	if r.SourceList == r.DestinationList {
		srcRows = [][protocol.CurrentItemRecordSize]byte{
			rowFor(r.SourceList, r.SourceSlot),
			rowFor(r.DestinationList, r.DestinationSlot),
		}
	} else {
		srcRows = [][protocol.CurrentItemRecordSize]byte{rowFor(r.SourceList, r.SourceSlot)}
		dstRows = [][protocol.CurrentItemRecordSize]byte{rowFor(r.DestinationList, r.DestinationSlot)}
	}
	if movedCount == 0 {
		movedCount = r.Count
		if movedCount == 0 {
			movedCount = 1
		}
	}
	return saved, movedCount, srcRows, dstRows, nil
}

// SortVault applies CMD 20 slot rearrangement pairs to Personal Safe I (0x02),
// Personal Safe II (0x2d), or Account Vault (0x0c) and returns a silent NOTI 13 restore.
func (s *VaultService) SortVault(
	ctx context.Context,
	role storage.Character,
	key string,
	space byte,
	moveMap map[uint16]uint16,
	destSeen map[uint16]bool,
) (storage.Character, []byte, error) {
	if s == nil || s.Store == nil {
		return role, nil, fmt.Errorf("vault service unavailable")
	}
	saved, pv, av, _, err := s.Store.CommitVaultOp(
		ctx,
		role.AccountID,
		role.ID,
		s.Rules.InitialSlots,
		s.Rules.SourceSHA256,
		role.ConfigVersion,
		key,
		"US224-vault-sort-v1",
		func(cur storage.Character, curPV storage.VaultState, curAV storage.AccountVaultState) (json.RawMessage, storage.VaultState, storage.AccountVaultState, json.RawMessage, error) {
			items1, err := ParseVaultItems(curPV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			items2, err := ParseVaultItems(curPV.Items2)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			accItems, err := ParseVaultItems(curAV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			list, capSlots, err := vaultSliceForSpace(space, curPV, curAV, &items1, &items2, &accItems)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			for src, dst := range moveMap {
				if src >= capSlots || dst >= capSlots {
					return nil, curPV, curAV, nil, fmt.Errorf("sort slot out of vault range %d->%d (cap=%d)", src, dst, capSlots)
				}
			}
			for _, it := range *list {
				if _, moving := moveMap[it.Slot]; !moving && destSeen[it.Slot] {
					return nil, curPV, curAV, nil, fmt.Errorf("sort destination vault slot %d occupied by unmoved item", it.Slot)
				}
			}
			for i := range *list {
				if dst, ok := moveMap[(*list)[i].Slot]; ok {
					(*list)[i].Slot = dst
				}
			}
			curPV.Items, err = MarshalVaultItems(items1)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			curPV.Items2, err = MarshalVaultItems(items2)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			curAV.Items, err = MarshalVaultItems(accItems)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			receipt, err := json.Marshal(map[string]any{"space": space, "pairs": len(moveMap)})
			return nil, curPV, curAV, receipt, err
		},
	)
	if err != nil {
		return role, nil, err
	}
	saved.WireID = role.WireID
	switch space {
	case protocol.PersonalVaultSpace:
		items1, err := ParseVaultItems(pv.Items)
		if err != nil {
			return saved, nil, err
		}
		p, err := protocol.US224PersonalVaultRestore(pv.Slots, VaultRows(items1))
		return saved, p, err
	case protocol.PersonalVault2Space:
		items2, err := ParseVaultItems(pv.Items2)
		if err != nil {
			return saved, nil, err
		}
		p, err := protocol.US224PersonalVault2Restore(pv.Slots2, VaultRows(items2))
		return saved, p, err
	case protocol.AccountVaultSpace:
		accItems, err := ParseVaultItems(av.Items)
		if err != nil {
			return saved, nil, err
		}
		p, err := protocol.US224AccountVaultRestore(av.Slots, av.Gold, VaultRows(accItems))
		return saved, p, err
	default:
		return saved, nil, fmt.Errorf("unsupported vault space %d", space)
	}
}

// SetSortLock toggles Ctrl+Click sort lock on an item inside a vault space.
func (s *VaultService) SetSortLock(ctx context.Context, role storage.Character, key string, space byte, slot uint16, locked bool) (storage.Character, error) {
	if s == nil || s.Store == nil {
		return role, fmt.Errorf("vault service unavailable")
	}
	saved, _, _, _, err := s.Store.CommitVaultOp(
		ctx,
		role.AccountID,
		role.ID,
		s.Rules.InitialSlots,
		s.Rules.SourceSHA256,
		role.ConfigVersion,
		key,
		"US224-vault-sort-lock-v1",
		func(cur storage.Character, curPV storage.VaultState, curAV storage.AccountVaultState) (json.RawMessage, storage.VaultState, storage.AccountVaultState, json.RawMessage, error) {
			items1, err := ParseVaultItems(curPV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			items2, err := ParseVaultItems(curPV.Items2)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			accItems, err := ParseVaultItems(curAV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			list, _, err := vaultSliceForSpace(space, curPV, curAV, &items1, &items2, &accItems)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			idx, _ := findVaultItem(*list, slot)
			if idx < 0 {
				return nil, curPV, curAV, nil, fmt.Errorf("no vault item at space=%d slot=%d", space, slot)
			}
			(*list)[idx].SortLocked = locked
			curPV.Items, _ = MarshalVaultItems(items1)
			curPV.Items2, _ = MarshalVaultItems(items2)
			curAV.Items, _ = MarshalVaultItems(accItems)
			receipt, _ := json.Marshal(map[string]any{"space": space, "slot": slot, "sort_locked": locked})
			return nil, curPV, curAV, receipt, nil
		},
	)
	saved.WireID = role.WireID
	return saved, err
}

// SetItemLock sets Alt+Click safety lock state on an item inside a vault space.
func (s *VaultService) SetItemLock(ctx context.Context, role storage.Character, key string, space byte, slot uint16, state byte, unlockAt int64) (storage.Character, error) {
	if s == nil || s.Store == nil {
		return role, fmt.Errorf("vault service unavailable")
	}
	saved, _, _, _, err := s.Store.CommitVaultOp(
		ctx,
		role.AccountID,
		role.ID,
		s.Rules.InitialSlots,
		s.Rules.SourceSHA256,
		role.ConfigVersion,
		key,
		"US224-vault-item-lock-v1",
		func(cur storage.Character, curPV storage.VaultState, curAV storage.AccountVaultState) (json.RawMessage, storage.VaultState, storage.AccountVaultState, json.RawMessage, error) {
			items1, err := ParseVaultItems(curPV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			items2, err := ParseVaultItems(curPV.Items2)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			accItems, err := ParseVaultItems(curAV.Items)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			list, _, err := vaultSliceForSpace(space, curPV, curAV, &items1, &items2, &accItems)
			if err != nil {
				return nil, curPV, curAV, nil, err
			}
			idx, _ := findVaultItem(*list, slot)
			if idx < 0 {
				return nil, curPV, curAV, nil, fmt.Errorf("no vault item at space=%d slot=%d", space, slot)
			}
			(*list)[idx].ItemLock = state
			(*list)[idx].UnlockAt = unlockAt
			curPV.Items, _ = MarshalVaultItems(items1)
			curPV.Items2, _ = MarshalVaultItems(items2)
			curAV.Items, _ = MarshalVaultItems(accItems)
			receipt, _ := json.Marshal(map[string]any{"space": space, "slot": slot, "item_lock": state})
			return nil, curPV, curAV, receipt, nil
		},
	)
	saved.WireID = role.WireID
	return saved, err
}

// LockedItemEntries collects active item safety locks across Personal Safe I/II and Account Vault.
func (s *VaultService) LockedItemEntries(ctx context.Context, role storage.Character, now int64) []protocol.ItemLockEntry {
	if s == nil || s.Store == nil {
		return nil
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var out []protocol.ItemLockEntry
	collect := func(space byte, raw json.RawMessage) {
		items, err := ParseVaultItems(raw)
		if err != nil {
			return
		}
		for _, it := range items {
			if it.ItemLock == 0 {
				continue
			}
			var rem uint32
			if it.ItemLock == 2 {
				if it.UnlockAt <= now {
					continue
				}
				diff := it.UnlockAt - now
				if diff > math.MaxUint32 {
					diff = math.MaxUint32
				}
				rem = uint32(diff)
			}
			out = append(out, protocol.ItemLockEntry{
				Space:            space,
				Slot:             it.Slot,
				State:            it.ItemLock,
				RemainingSeconds: rem,
			})
		}
	}
	if v, err := s.Store.LoadVault(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, s.Rules.SourceSHA256); err == nil {
		collect(protocol.PersonalVaultSpace, v.Items)
		collect(protocol.PersonalVault2Space, v.Items2)
	}
	if av, err := s.Store.LoadAccountVault(ctx, role.AccountID); err == nil && av.Slots > 0 {
		collect(protocol.AccountVaultSpace, av.Items)
	}
	return out
}

// UpgradePersonal advances Personal Safe I (tab=0) or Personal Safe II (tab=1)
// to targetSlots (or next tier when targetSlots==0) and persists it in us100.db.
func (s *VaultService) UpgradePersonal(ctx context.Context, role storage.Character, tab byte, targetSlots uint16) (uint16, error) {
	if s == nil || s.Store == nil {
		return 0, fmt.Errorf("vault service unavailable")
	}
	v, err := s.Store.LoadVault(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, s.Rules.SourceSHA256)
	if err != nil {
		return 0, err
	}
	cur := v.Slots
	if tab == 1 {
		cur = v.Slots2
	}
	next, err := NextPersonalVaultSlots(cur, targetSlots)
	if err != nil {
		return cur, err
	}
	saved, err := s.Store.SavePersonalVaultSlots(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, next, tab, s.Rules.SourceSHA256)
	if err != nil {
		return cur, err
	}
	if tab == 1 {
		return saved.Slots2, nil
	}
	return saved.Slots, nil
}

// UpgradeAccount advances the account's shared Account Vault (space 0x0c) to
// targetSlots (or next +8 tier when targetSlots==0) and persists it.
func (s *VaultService) UpgradeAccount(ctx context.Context, account int64, targetSlots uint16) (storage.AccountVaultState, error) {
	if s == nil || s.Store == nil {
		return storage.AccountVaultState{}, fmt.Errorf("vault service unavailable")
	}
	cur, err := s.Store.LoadAccountVault(ctx, account)
	if err != nil {
		return cur, err
	}
	next, err := NextAccountVaultSlots(cur.Slots, targetSlots)
	if err != nil {
		return cur, err
	}
	return s.Store.SaveAccountVaultSlots(ctx, account, next)
}
