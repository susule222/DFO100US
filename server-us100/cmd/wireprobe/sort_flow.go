package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"time"
)

func sortInventory(
	ctx context.Context,
	store *storage.Store,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
) (storage.Character, []outboundPacket, byte, error) {
	return sortInventoryWithVaults(ctx, store, nil, account, role, rawFrame, plaintext)
}

func sortInventoryWithVaults(
	ctx context.Context,
	store *storage.Store,
	vaults *inventory.VaultService,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
) (storage.Character, []outboundPacket, byte, error) {
	req, err := protocol.DecodeSortItems(plaintext)
	if err != nil {
		return role, nil, 0, err
	}
	isVault := protocol.IsVaultSpace(req.Space)
	if req.Space != 0 && req.Space != 1 && req.Space != protocol.CreatureSpace && req.Space != protocol.InsigniaSpace && req.Space != protocol.TalismanSpace && !isVault {
		return role, nil, req.Space, fmt.Errorf("unsupported sort inventory space %d", req.Space)
	}
	moveMap := make(map[uint16]uint16, len(req.Pairs))
	destSeen := make(map[uint16]bool, len(req.Pairs))
	for _, pair := range req.Pairs {
		if req.Space == 0 && (pair.From < 9 || pair.To < 9 || pair.From > inventory.EmblemSlotEnd || pair.To > inventory.EmblemSlotEnd) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of ordinary bag range: %d->%d", pair.From, pair.To)
		}
		if req.Space == 1 && (pair.From >= inventory.AvatarBagCapacity || pair.To >= inventory.AvatarBagCapacity) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of avatar bag range: %d->%d", pair.From, pair.To)
		}
		if req.Space == protocol.CreatureSpace && (pair.From >= inventory.CreatureBagCapacity || pair.To >= inventory.CreatureBagCapacity) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of creature bag range: %d->%d", pair.From, pair.To)
		}
		if req.Space == protocol.InsigniaSpace && (pair.From >= inventory.InsigniaBagCapacity || pair.To >= inventory.InsigniaBagCapacity) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of insignia bag range: %d->%d", pair.From, pair.To)
		}
		if req.Space == protocol.TalismanSpace && (pair.From >= inventory.EquippedRuneStart || pair.To >= inventory.EquippedRuneStart) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of talisman/rune bag range: %d->%d", pair.From, pair.To)
		}
		if _, dup := moveMap[pair.From]; dup {
			return role, nil, req.Space, fmt.Errorf("duplicate sort source slot %d", pair.From)
		}
		if destSeen[pair.To] {
			return role, nil, req.Space, fmt.Errorf("duplicate sort destination slot %d", pair.To)
		}
		moveMap[pair.From] = pair.To
		destSeen[pair.To] = true
	}

	key := fmt.Sprintf("sort20/%d/%x", role.ID, sha256.Sum256(rawFrame))
	if isVault {
		if vaults == nil {
			return role, nil, req.Space, fmt.Errorf("vault service unavailable for sort space %d", req.Space)
		}
		saved, invRestore, err := vaults.SortVault(ctx, role, key, req.Space, moveMap, destSeen)
		if err != nil {
			return role, nil, req.Space, err
		}
		plan := []outboundPacket{
			{"sort_vault_restored", 0, 13, invRestore},
		}
		if lockBody, lockErr := itemLocksOnEntryWithVaults(ctx, saved, vaults); lockErr == nil && len(lockBody) > 0 {
			plan = append(plan, outboundPacket{"item_locks_restored", 0, protocol.NotiItemLockList, lockBody})
		}
		plan = append(plan, outboundPacket{"sort_items_ack", 1, 20, protocol.SortItemsSuccess(req.Space)})
		return saved, plan, req.Space, nil
	}
	saved, _, err := store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "US224-item-sort-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		if req.Space == 0 {
			// Verify no unmoved item sits on a destination slot.
			for _, it := range b.Items {
				if _, moving := moveMap[it.Slot]; !moving && destSeen[it.Slot] {
					return nil, nil, fmt.Errorf("sort destination slot %d occupied by unmoved item", it.Slot)
				}
			}
			for _, eq := range b.Equipment {
				if _, moving := moveMap[eq.Slot]; !moving && destSeen[eq.Slot] {
					return nil, nil, fmt.Errorf("sort destination slot %d occupied by unmoved equipment", eq.Slot)
				}
			}
			for i := range b.Items {
				if dst, ok := moveMap[b.Items[i].Slot]; ok {
					b.Items[i].Slot = dst
				}
			}
			for i := range b.Equipment {
				if dst, ok := moveMap[b.Equipment[i].Slot]; ok {
					b.Equipment[i].Slot = dst
				}
			}
		} else if req.Space == 1 {
			for _, av := range b.Avatars {
				if _, moving := moveMap[av.Slot]; !moving && destSeen[av.Slot] {
					return nil, nil, fmt.Errorf("sort destination avatar slot %d occupied by unmoved avatar", av.Slot)
				}
			}
			for i := range b.Avatars {
				if dst, ok := moveMap[b.Avatars[i].Slot]; ok {
					b.Avatars[i].Slot = dst
				}
			}
		} else if req.Space == protocol.CreatureSpace {
			for _, cr := range b.Creatures {
				if _, moving := moveMap[cr.Slot]; !moving && destSeen[cr.Slot] {
					return nil, nil, fmt.Errorf("sort destination creature slot %d occupied by unmoved creature", cr.Slot)
				}
			}
			for _, ca := range b.CreatureArtifacts {
				if _, moving := moveMap[ca.Slot]; !moving && destSeen[ca.Slot] {
					return nil, nil, fmt.Errorf("sort destination artifact slot %d occupied by unmoved artifact", ca.Slot)
				}
			}
			for _, ci := range b.CreatureItems {
				if _, moving := moveMap[ci.Slot]; !moving && destSeen[ci.Slot] {
					return nil, nil, fmt.Errorf("sort destination creature item slot %d occupied by unmoved item", ci.Slot)
				}
			}
			for i := range b.Creatures {
				if dst, ok := moveMap[b.Creatures[i].Slot]; ok {
					b.Creatures[i].Slot = dst
				}
			}
			for i := range b.CreatureArtifacts {
				if dst, ok := moveMap[b.CreatureArtifacts[i].Slot]; ok {
					b.CreatureArtifacts[i].Slot = dst
				}
			}
			for i := range b.CreatureItems {
				if dst, ok := moveMap[b.CreatureItems[i].Slot]; ok {
					b.CreatureItems[i].Slot = dst
				}
			}
		} else if req.Space == protocol.InsigniaSpace {
			for _, ins := range b.Insignias {
				if _, moving := moveMap[ins.Slot]; !moving && destSeen[ins.Slot] {
					return nil, nil, fmt.Errorf("sort destination insignia slot %d occupied by unmoved insignia", ins.Slot)
				}
			}
			for _, gm := range b.Gems {
				if _, moving := moveMap[gm.Slot]; !moving && destSeen[gm.Slot] {
					return nil, nil, fmt.Errorf("sort destination gem slot %d occupied by unmoved gem", gm.Slot)
				}
			}
			for i := range b.Insignias {
				if dst, ok := moveMap[b.Insignias[i].Slot]; ok {
					b.Insignias[i].Slot = dst
				}
			}
			for i := range b.Gems {
				if dst, ok := moveMap[b.Gems[i].Slot]; ok {
					b.Gems[i].Slot = dst
				}
			}
		} else {
			for _, tl := range b.Talismans {
				if _, moving := moveMap[tl.Slot]; !moving && destSeen[tl.Slot] {
					return nil, nil, fmt.Errorf("sort destination talisman slot %d occupied by unmoved talisman", tl.Slot)
				}
			}
			for _, rn := range b.Runes {
				if _, moving := moveMap[rn.Slot]; !moving && destSeen[rn.Slot] {
					return nil, nil, fmt.Errorf("sort destination rune slot %d occupied by unmoved rune", rn.Slot)
				}
			}
			for i := range b.Talismans {
				if dst, ok := moveMap[b.Talismans[i].Slot]; ok {
					b.Talismans[i].Slot = dst
				}
			}
			for i := range b.Runes {
				if dst, ok := moveMap[b.Runes[i].Slot]; ok {
					b.Runes[i].Slot = dst
				}
			}
		}
		state, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"space": req.Space, "tab": req.Tab, "pairs": len(req.Pairs)})
		return state, receipt, e
	})
	if err != nil {
		return role, nil, req.Space, err
	}

	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		return saved, nil, req.Space, err
	}

	// Use NOTI 13 (silent full-space restore) instead of NOTI 14: NOTI 14
	// hardcodes showAcquireToast=1 (0x1435107af) and prints "获得 N 个 xxx"
	// system chat notices for every item that lands in a new slot.
	var invRestore []byte
	if req.Space == 0 {
		invRestore, err = protocol.US224InventoryRestoreWithExpansion(bag.ExpandedSlots, bag.Rows())
	} else if req.Space == 1 {
		invRestore, err = protocol.US224AvatarRestore(inventory.AvatarBagCapacity, bag.AvatarRows())
	} else if req.Space == protocol.CreatureSpace {
		invRestore, err = protocol.US224CreatureRestore(bag.CreatureRows())
	} else if req.Space == protocol.InsigniaSpace {
		invRestore, err = protocol.US224InsigniaRestore(bag.InsigniaRows())
	} else {
		invRestore, err = protocol.US224TalismanRestore(bag.TalismanRows())
	}
	if err != nil {
		return saved, nil, req.Space, err
	}

	saved.WireID = role.WireID
	plan := []outboundPacket{
		{"sort_inventory_restored", 0, 13, invRestore},
	}
	if vaults != nil {
		if lockBody, lockErr := itemLocksOnEntryWithVaults(ctx, saved, vaults); lockErr == nil && len(lockBody) > 0 {
			plan = append(plan, outboundPacket{"item_locks_restored", 0, protocol.NotiItemLockList, lockBody})
		}
	} else if locks := bag.LockedItemEntries(time.Now().Unix()); len(locks) > 0 {
		if lockBody, lockErr := protocol.ItemLockListNoti(locks); lockErr == nil {
			plan = append(plan, outboundPacket{"item_locks_restored", 0, protocol.NotiItemLockList, lockBody})
		}
	}
	plan = append(plan, outboundPacket{"sort_items_ack", 1, 20, protocol.SortItemsSuccess(req.Space)})
	return saved, plan, req.Space, nil
}
