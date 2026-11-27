package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

func sortInventory(
	ctx context.Context,
	store *storage.Store,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
) (storage.Character, []outboundPacket, byte, error) {
	req, err := protocol.DecodeSortItems(plaintext)
	if err != nil {
		return role, nil, 0, err
	}
	if req.Space != 0 && req.Space != 1 {
		return role, nil, req.Space, fmt.Errorf("unsupported sort inventory space %d", req.Space)
	}
	moveMap := make(map[uint16]uint16, len(req.Pairs))
	destSeen := make(map[uint16]bool, len(req.Pairs))
	for _, pair := range req.Pairs {
		if req.Space == 0 && (pair.From < 9 || pair.To < 9 || pair.From > 288 || pair.To > 288) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of ordinary bag range: %d->%d", pair.From, pair.To)
		}
		if req.Space == 1 && (pair.From >= inventory.AvatarBagCapacity || pair.To >= inventory.AvatarBagCapacity) {
			return role, nil, req.Space, fmt.Errorf("sort slot out of avatar bag range: %d->%d", pair.From, pair.To)
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
		} else {
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
		invRestore, err = protocol.US224InventoryRestore(bag.Rows())
	} else {
		invRestore, err = protocol.US224AvatarRestore(inventory.AvatarBagCapacity, bag.AvatarRows())
	}
	if err != nil {
		return saved, nil, req.Space, err
	}

	saved.WireID = role.WireID
	plan := []outboundPacket{
		{"sort_inventory_restored", 0, 13, invRestore},
		{"sort_items_ack", 1, 20, protocol.SortItemsSuccess(req.Space)},
	}
	return saved, plan, req.Space, nil
}
