package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errDeleteItemLocked = errors.New("cannot delete safety-locked item")

// deleteInventoryItems handles CMD 18 (0x12, ENUM_CMDPACKET_DELETE_ITEM).
//
// The US100 client pushes a transaction lock onto InvenMgr (+0x1c0) immediately
// upon sending CMD 18 (0x143b25b31) and blocks booster opening (0x144002aeb),
// moving, sorting, and further deletions until ACK 18 arrives to pop the lock
// (0x143b19a80) and close confirmation window 0x3bf.
//
// Outbound packet order: ACK 18 MUST precede NOTI 13 so InvenMgr::DeleteItems
// (0x143b194c0) still sees each target slot locally before applying the removal
// and popping the lock; NOTI 13 then silently synchronizes the authoritative bag.
func deleteInventoryItems(
	ctx context.Context,
	store *storage.Store,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
) (storage.Character, []outboundPacket, byte, uint16, error) {
	req, err := protocol.DecodeDeleteItems(plaintext)
	if err != nil {
		return role, nil, 0, 0x11, err
	}
	if req.Space != 0 && req.Space != 1 && req.Space != protocol.TalismanSpace {
		return role, nil, req.Space, 0x11, fmt.Errorf("unsupported delete inventory space %d", req.Space)
	}

	deleted := make([]protocol.DeletedItem, 0, len(req.Items))
	key := fmt.Sprintf("delete18/%d/%x", role.ID, sha256.Sum256(rawFrame))
	now := time.Now().Unix()

	saved, _, err := store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "US224-item-delete-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		lockedSlots := make(map[uint16]bool)
		if req.Space == 0 {
			for _, lk := range b.LockedItemEntries(now) {
				if lk.Space == 0 {
					lockedSlots[lk.Slot] = true
				}
			}
		}

		batch := make([]protocol.DeletedItem, 0, len(req.Items))
		for _, want := range req.Items {
			if lockedSlots[want.Slot] {
				return nil, nil, fmt.Errorf("%w: slot %d", errDeleteItemLocked, want.Slot)
			}
			switch req.Space {
			case 0:
				removed := false
				for i, it := range b.Items {
					if it.Slot == want.Slot {
						if want.Template != 0 && it.Template != want.Template {
							return nil, nil, fmt.Errorf("slot %d holds template %d, want %d", want.Slot, it.Template, want.Template)
						}
						if it.Amount < want.Count {
							return nil, nil, fmt.Errorf("slot %d has %d, cannot delete %d", want.Slot, it.Amount, want.Count)
						}
						if it.Amount == want.Count {
							b.Items = append(b.Items[:i], b.Items[i+1:]...)
						} else {
							b.Items[i].Amount -= want.Count
						}
						removed = true
						break
					}
				}
				if !removed {
					for i, eq := range b.Equipment {
						if eq.Slot == want.Slot {
							if want.Template != 0 && eq.Template != want.Template {
								return nil, nil, fmt.Errorf("equipment slot %d holds template %d, want %d", want.Slot, eq.Template, want.Template)
							}
							if want.Count != 1 {
								return nil, nil, fmt.Errorf("equipment slot %d delete count must be 1, got %d", want.Slot, want.Count)
							}
							b.Equipment = append(b.Equipment[:i], b.Equipment[i+1:]...)
							removed = true
							break
						}
					}
				}
				if !removed {
					return nil, nil, fmt.Errorf("slot %d is empty in space 0", want.Slot)
				}
			case 1:
				removed := false
				for i, av := range b.Avatars {
					if av.Slot == want.Slot {
						if want.Template != 0 && av.Template != want.Template {
							return nil, nil, fmt.Errorf("avatar slot %d holds template %d, want %d", want.Slot, av.Template, want.Template)
						}
						b.Avatars = append(b.Avatars[:i], b.Avatars[i+1:]...)
						removed = true
						break
					}
				}
				if !removed {
					return nil, nil, fmt.Errorf("avatar slot %d is empty", want.Slot)
				}
			case protocol.TalismanSpace:
				removed := false
				for i, tl := range b.Talismans {
					if tl.Slot == want.Slot {
						if want.Template != 0 && tl.Template != want.Template {
							return nil, nil, fmt.Errorf("talisman slot %d holds template %d, want %d", want.Slot, tl.Template, want.Template)
						}
						b.Talismans = append(b.Talismans[:i], b.Talismans[i+1:]...)
						removed = true
						break
					}
				}
				if !removed {
					for i, rn := range b.Runes {
						if rn.Slot == want.Slot {
							if want.Template != 0 && rn.Template != want.Template {
								return nil, nil, fmt.Errorf("rune slot %d holds template %d, want %d", want.Slot, rn.Template, want.Template)
							}
							if rn.Amount > want.Count {
								b.Runes[i].Amount -= want.Count
							} else {
								b.Runes = append(b.Runes[:i], b.Runes[i+1:]...)
							}
							removed = true
							break
						}
					}
				}
				if !removed {
					return nil, nil, fmt.Errorf("talisman/rune slot %d is empty", want.Slot)
				}
			}
			batch = append(batch, protocol.DeletedItem{
				Slot:       want.Slot,
				Count:      want.Count,
				DeleteType: want.DeleteType,
			})
		}

		state, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"space": req.Space, "items": batch})
		return state, receipt, e
	})
	if err != nil {
		code := uint16(0x11)
		if errors.Is(err, errDeleteItemLocked) {
			code = 0x13
		}
		return role, nil, req.Space, code, err
	}

	for _, want := range req.Items {
		deleted = append(deleted, protocol.DeletedItem{
			Slot:       want.Slot,
			Count:      want.Count,
			DeleteType: want.DeleteType,
		})
	}

	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		return saved, nil, req.Space, 0x11, err
	}
	var invRestore []byte
	switch req.Space {
	case 0:
		invRestore, err = protocol.US224InventoryRestoreWithExpansion(bag.ExpandedSlots, bag.Rows())
	case 1:
		invRestore, err = protocol.US224AvatarRestore(inventory.AvatarBagCapacity, bag.AvatarRows())
	case protocol.TalismanSpace:
		invRestore, err = protocol.US224TalismanRestore(bag.TalismanRows())
	}
	if err != nil {
		return saved, nil, req.Space, 0x11, err
	}

	saved.WireID = role.WireID
	plan := []outboundPacket{
		{"delete_items_ack", 1, protocol.CmdDeleteItem, protocol.DeleteItemsSuccess(req.Space, deleted)},
		{"delete_inventory_restored", 0, 13, invRestore},
	}
	if req.Space == 0 {
		if locks := bag.LockedItemEntries(now); len(locks) > 0 {
			if lockBody, lockErr := protocol.ItemLockListNoti(locks); lockErr == nil {
				plan = append(plan, outboundPacket{"item_locks_restored", 0, protocol.NotiItemLockList, lockBody})
			}
		}
		if djUpd, djErr := danjiSpaceUpdatePayload(bag); djErr == nil {
			plan = append(plan, outboundPacket{"delete_space2a_updated", 0, 14, djUpd})
		}
	}
	return saved, plan, req.Space, 0, nil
}
