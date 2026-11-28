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

func setSortLock(
	ctx context.Context,
	store *storage.Store,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
	locked bool,
) (storage.Character, []outboundPacket, error) {
	return setSortLockWithVaults(ctx, store, nil, account, role, rawFrame, plaintext, locked)
}

func setSortLockWithVaults(
	ctx context.Context,
	store *storage.Store,
	vaults *inventory.VaultService,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
	locked bool,
) (storage.Character, []outboundPacket, error) {
	req, err := protocol.DecodeItemSlotTarget(plaintext)
	if err != nil {
		return role, nil, err
	}
	isVault := protocol.IsVaultSpace(req.Space)
	if req.Space != 0 && req.Space != 1 && req.Space != protocol.CreatureSpace && req.Space != protocol.InsigniaSpace && req.Space != protocol.TalismanSpace && !isVault {
		return role, nil, fmt.Errorf("unsupported sort lock space %d", req.Space)
	}
	cmdID := protocol.CmdSortUnlock
	ackName := "sort_unlock_ack"
	if locked {
		cmdID = protocol.CmdSortLock
		ackName = "sort_lock_ack"
	}
	key := fmt.Sprintf("sortlock%d/%d/%x", cmdID, role.ID, sha256.Sum256(rawFrame))
	if isVault {
		if vaults == nil {
			return role, nil, fmt.Errorf("vault service unavailable for sort lock space %d", req.Space)
		}
		saved, err := vaults.SetSortLock(ctx, role, key, req.Space, req.Slot, locked)
		if err != nil {
			return role, nil, err
		}
		return saved, []outboundPacket{
			{ackName, 1, cmdID, protocol.SortLockSuccess(req.Space, req.Slot)},
		}, nil
	}
	saved, _, err := store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "US224-item-sort-lock-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		found := false
		if req.Space == 0 {
			for i := range b.Items {
				if b.Items[i].Slot == req.Slot {
					b.Items[i].SortLocked = locked
					found = true
					break
				}
			}
			if !found {
				for i := range b.Equipment {
					if b.Equipment[i].Slot == req.Slot {
						b.Equipment[i].SortLocked = locked
						found = true
						break
					}
				}
			}
		} else if req.Space == 1 {
			for i := range b.Avatars {
				if b.Avatars[i].Slot == req.Slot {
					b.Avatars[i].SortLocked = locked
					found = true
					break
				}
			}
		} else if req.Space == protocol.CreatureSpace {
			for i := range b.Creatures {
				if b.Creatures[i].Slot == req.Slot {
					b.Creatures[i].SortLocked = locked
					found = true
					break
				}
			}
			if !found {
				for i := range b.CreatureArtifacts {
					if b.CreatureArtifacts[i].Slot == req.Slot {
						b.CreatureArtifacts[i].SortLocked = locked
						found = true
						break
					}
				}
			}
			if !found {
				for i := range b.CreatureItems {
					if b.CreatureItems[i].Slot == req.Slot {
						b.CreatureItems[i].SortLocked = locked
						found = true
						break
					}
				}
			}
		} else if req.Space == protocol.InsigniaSpace {
			for i := range b.Insignias {
				if b.Insignias[i].Slot == req.Slot {
					b.Insignias[i].SortLocked = locked
					found = true
					break
				}
			}
			if !found {
				for i := range b.Gems {
					if b.Gems[i].Slot == req.Slot {
						b.Gems[i].SortLocked = locked
						found = true
						break
					}
				}
			}
		} else if req.Space == protocol.TalismanSpace {
			for i := range b.Talismans {
				if b.Talismans[i].Slot == req.Slot {
					b.Talismans[i].SortLocked = locked
					found = true
					break
				}
			}
			if !found {
				for i := range b.Runes {
					if b.Runes[i].Slot == req.Slot {
						b.Runes[i].SortLocked = locked
						found = true
						break
					}
				}
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("no item at space=%d slot=%d", req.Space, req.Slot)
		}
		state, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"space": req.Space, "slot": req.Slot, "sort_locked": locked})
		return state, receipt, e
	})
	if err != nil {
		return role, nil, err
	}
	saved.WireID = role.WireID
	return saved, []outboundPacket{
		{ackName, 1, cmdID, protocol.SortLockSuccess(req.Space, req.Slot)},
	}, nil
}

func mutateBagItemLock(b *inventory.Bag, space byte, slot uint16, state byte, unlockAt int64) bool {
	switch space {
	case 0:
		for i := range b.Items {
			if b.Items[i].Slot == slot {
				b.Items[i].ItemLock = state
				b.Items[i].UnlockAt = unlockAt
				return true
			}
		}
		for i := range b.Equipment {
			if b.Equipment[i].Slot == slot {
				b.Equipment[i].ItemLock = state
				b.Equipment[i].UnlockAt = unlockAt
				return true
			}
		}
	case 1:
		for i := range b.Avatars {
			if b.Avatars[i].Slot == slot {
				b.Avatars[i].ItemLock = state
				b.Avatars[i].UnlockAt = unlockAt
				return true
			}
		}
	case 3:
		for i := range b.Worn {
			if b.Worn[i].Slot == slot {
				b.Worn[i].ItemLock = state
				b.Worn[i].UnlockAt = unlockAt
				return true
			}
		}
		for i := range b.WornAvatars {
			if b.WornAvatars[i].Slot == slot {
				b.WornAvatars[i].ItemLock = state
				b.WornAvatars[i].UnlockAt = unlockAt
				return true
			}
		}
	case protocol.CreatureSpace:
		for i := range b.Creatures {
			if b.Creatures[i].Slot == slot {
				b.Creatures[i].ItemLock = state
				b.Creatures[i].UnlockAt = unlockAt
				return true
			}
		}
		for i := range b.CreatureArtifacts {
			if b.CreatureArtifacts[i].Slot == slot {
				b.CreatureArtifacts[i].ItemLock = state
				b.CreatureArtifacts[i].UnlockAt = unlockAt
				return true
			}
		}
		for i := range b.CreatureItems {
			if b.CreatureItems[i].Slot == slot {
				b.CreatureItems[i].ItemLock = state
				b.CreatureItems[i].UnlockAt = unlockAt
				return true
			}
		}
	case protocol.InsigniaSpace:
		for i := range b.Insignias {
			if b.Insignias[i].Slot == slot {
				b.Insignias[i].ItemLock = state
				b.Insignias[i].UnlockAt = unlockAt
				return true
			}
		}
		for i := range b.Gems {
			if b.Gems[i].Slot == slot {
				b.Gems[i].ItemLock = state
				b.Gems[i].UnlockAt = unlockAt
				return true
			}
		}
	case protocol.TalismanSpace:
		for i := range b.Talismans {
			if b.Talismans[i].Slot == slot {
				b.Talismans[i].ItemLock = state
				b.Talismans[i].UnlockAt = unlockAt
				return true
			}
		}
		for i := range b.Runes {
			if b.Runes[i].Slot == slot {
				b.Runes[i].ItemLock = state
				b.Runes[i].UnlockAt = unlockAt
				return true
			}
		}
	}
	return false
}

func handleItemLockCommand(
	ctx context.Context,
	store *storage.Store,
	account int64,
	role storage.Character,
	cmdID uint16,
	rawFrame []byte,
	plaintext []byte,
) (storage.Character, []outboundPacket, error) {
	return handleItemLockWithVaults(ctx, store, nil, account, role, cmdID, rawFrame, plaintext)
}

func handleItemLockWithVaults(
	ctx context.Context,
	store *storage.Store,
	vaults *inventory.VaultService,
	account int64,
	role storage.Character,
	cmdID uint16,
	rawFrame []byte,
	plaintext []byte,
) (storage.Character, []outboundPacket, error) {
	var space byte
	var slot uint16
	var targetState byte
	switch cmdID {
	case protocol.CmdItemLock, protocol.CmdItemCancelUnlock:
		req, err := protocol.DecodeItemSlotTarget(plaintext)
		if err != nil {
			return role, nil, err
		}
		space, slot, targetState = req.Space, req.Slot, 1
	case protocol.CmdItemUnlock:
		req, err := protocol.DecodeItemUnlock(plaintext)
		if err != nil {
			return role, nil, err
		}
		space, slot, targetState = req.Space, req.Slot, 0
	case protocol.CmdItemUnlockNow:
		req, err := protocol.DecodeItemUnlockNow(plaintext)
		if err != nil {
			return role, nil, err
		}
		space, slot, targetState = req.Space, req.Slot, 0
	default:
		return role, nil, fmt.Errorf("unsupported item lock cmd %d", cmdID)
	}
	isVault := protocol.IsVaultSpace(space)
	if space != 0 && space != 1 && space != 3 && space != protocol.CreatureSpace && space != protocol.InsigniaSpace && space != protocol.TalismanSpace && !isVault {
		return role, nil, fmt.Errorf("unsupported item lock space %d", space)
	}

	key := fmt.Sprintf("itemlock%d/%d/%x", cmdID, role.ID, sha256.Sum256(rawFrame))
	var saved storage.Character
	var err error
	if isVault {
		if vaults == nil {
			return role, nil, fmt.Errorf("vault service unavailable for item lock space %d", space)
		}
		saved, err = vaults.SetItemLock(ctx, role, key, space, slot, targetState, 0)
	} else {
		saved, _, err = store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "US224-item-lock-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
			b, e := inventory.ReadBag(current.State)
			if e != nil {
				return nil, nil, e
			}
			if !mutateBagItemLock(&b, space, slot, targetState, 0) {
				return nil, nil, fmt.Errorf("no item at space=%d slot=%d", space, slot)
			}
			state, e := inventory.SaveBag(current.State, b)
			if e != nil {
				return nil, nil, e
			}
			receipt, e := json.Marshal(map[string]any{"cmd": cmdID, "space": space, "slot": slot, "item_lock": targetState})
			return state, receipt, e
		})
	}
	if err != nil {
		return role, nil, err
	}
	saved.WireID = role.WireID
	switch cmdID {
	case protocol.CmdItemLock:
		return saved, []outboundPacket{{"item_lock_ack", 1, protocol.CmdItemLock, protocol.ItemLockSuccess(space, slot)}}, nil
	case protocol.CmdItemCancelUnlock:
		return saved, []outboundPacket{{"item_cancel_unlock_ack", 1, protocol.CmdItemCancelUnlock, protocol.ItemLockSuccess(space, slot)}}, nil
	case protocol.CmdItemUnlockNow:
		return saved, []outboundPacket{{"item_unlock_now_ack", 1, protocol.CmdItemUnlockNow, protocol.ItemLockSuccess(space, slot)}}, nil
	case protocol.CmdItemUnlock:
		// Unlock immediately on local/LAN server: ACK 268 (0s) + NOTI 252 clears
		// client lock state to 0 so players are not stuck waiting 72 hours.
		return saved, []outboundPacket{
			{"item_unlock_ack", 1, protocol.CmdItemUnlock, protocol.ItemUnlockSuccess(space, slot, 0)},
			{"item_unlocked_noti", 0, protocol.NotiItemUnlocked, protocol.ItemUnlockedNoti(space, slot)},
		}, nil
	}
	return saved, nil, nil
}

func itemLocksOnEntry(state json.RawMessage) ([]byte, error) {
	b, err := inventory.ReadBag(state)
	if err != nil {
		return nil, err
	}
	entries := b.LockedItemEntries(time.Now().Unix())
	if len(entries) == 0 {
		return nil, nil
	}
	return protocol.ItemLockListNoti(entries)
}

func itemLocksOnEntryWithVaults(ctx context.Context, role storage.Character, vaults *inventory.VaultService) ([]byte, error) {
	b, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	entries := b.LockedItemEntries(now)
	if vaults != nil {
		entries = append(entries, vaults.LockedItemEntries(ctx, role, now)...)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return protocol.ItemLockListNoti(entries)
}
