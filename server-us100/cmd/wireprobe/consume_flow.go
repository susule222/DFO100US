package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// useStackable answers CMD44. The client applies potion/recovery effects itself,
// while contract items also activate their account contract via NOTI 66.
func (w *worldSession) useStackable(p, rawFrame []byte) ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 || w.loot == nil {
		return nil, fmt.Errorf("item use before character selection")
	}
	r, e := protocol.DecodeUseStackable(p)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := ""
	if len(rawFrame) > 0 {
		key = fmt.Sprintf("consume44/%d/%x", w.role.ID, sha256.Sum256(rawFrame))
	}
	saved, receipt, applied, e := w.loot.ConsumeWithKey(ctx, w.role, r, key)
	if e != nil {
		return nil, e
	}
	ack, e := protocol.UseStackableSuccess(r)
	if e != nil {
		return nil, e
	}
	b, e := inventory.ReadBag(saved.State)
	if e != nil {
		return nil, e
	}
	rows := b.Rows()
	if receipt.Remaining == 0 {
		rows = append(rows, protocol.EmptySlotItem(r.Slot))
		sort.Slice(rows, func(i, j int) bool {
			return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
		})
	}
	update, e := protocol.US224InventoryUpdate(rows)
	if e != nil {
		return nil, e
	}
	w.role = saved
	plan := []outboundPacket{
		{"item_use_ack", 1, 44, ack},
		{"item_use_inventory_updated", 0, 14, update},
	}
	if kind, days, ok := directContractItem(r.Template); ok {
		if applied {
			if err := w.loot.Store.ExtendPremiumContracts(ctx, w.account, map[byte]int64{kind: days}); err != nil {
				return nil, err
			}
		}
		if cpkts, cerr := buildContractRefreshPackets(ctx, w.loot.Store, w.fatigue, w.account, w.role.ID, map[byte]bool{kind: true}); cerr == nil {
			plan = append(plan, cpkts...)
		}
	}
	if vkind, vtarget, ok := vaultUpgradeTarget(r.Template); ok && w.vaults != nil {
		if vpkts, verr := applyVaultUpgrade(ctx, w.vaults, w.account, w.role, vkind, vtarget, applied); verr == nil {
			plan = append(plan, vpkts...)
		}
	}
	return plan, nil
}

// useStackableAction answers CMD 507 (0x1fb) for [action type] consumables such as
// Event 619 Epic Drop Buff Elixirs (10326700..10326704) and other special waste items.
func (w *worldSession) useStackableAction(p, rawFrame []byte) ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 || w.loot == nil || w.characters == nil {
		return nil, fmt.Errorf("item action use before character selection")
	}
	r, e := protocol.DecodeUseStackableAction(p)
	if e != nil {
		return nil, e
	}
	if r.List != 0 {
		return nil, fmt.Errorf("unsupported action item container %d", r.List)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("consume507/%d/%d/%d/%x", w.role.ID, r.Slot, r.ActionType, sha256.Sum256(rawFrame))

	var usedTemplate uint32
	var remaining uint32
	var expandedSlots uint16
	var didExpand bool
	saved, applied, e := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, key, "us100-consume-507-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, err := inventory.ReadBag(current.State)
		if err != nil {
			return nil, nil, err
		}
		idx := -1
		for i, it := range b.Items {
			if it.Slot == r.Slot && it.Amount > 0 {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, nil, fmt.Errorf("no stackable at slot %d", r.Slot)
		}
		usedTemplate = b.Items[idx].Template
		if target, ok := inventoryExpandTarget(usedTemplate); ok || r.ActionType == 0x28 {
			nextSlots, expErr := b.ExpandInventory(target)
			if expErr != nil {
				return nil, nil, expErr
			}
			expandedSlots = nextSlots
			didExpand = true
		}
		if b.Items[idx].Amount > 1 {
			b.Items[idx].Amount--
			remaining = b.Items[idx].Amount
		} else {
			b.Items = append(b.Items[:idx], b.Items[idx+1:]...)
			remaining = 0
		}
		updated, err := inventory.SaveBag(current.State, b)
		if err != nil {
			return nil, nil, err
		}
		receipt, err := json.Marshal(map[string]any{"slot": r.Slot, "template": usedTemplate, "action": r.ActionType, "remaining": remaining, "expanded_slots": expandedSlots})
		return updated, receipt, err
	})
	if e != nil {
		return nil, e
	}
	saved.WireID = w.role.WireID
	w.role = saved

	b, e := inventory.ReadBag(saved.State)
	if e != nil {
		return nil, e
	}
	rows := b.Rows()
	if remaining == 0 {
		rows = append(rows, protocol.EmptySlotItem(r.Slot))
		sort.Slice(rows, func(i, j int) bool {
			return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
		})
	}
	update, e := protocol.US224InventoryUpdate(rows)
	if e != nil {
		return nil, e
	}
	var plan []outboundPacket
	// For [add skin storage] (action=170), ACK 507 immediately triggers the
	// client's Skin Storage UI update (0x143a03f60 -> 0x142848ec0), so NOTI
	// 1545 (owned list) and NOTI 1547 (Recently Acquired) MUST precede ACK 507.
	if r.ActionType == actionTypeAddSkinStorage {
		plan = append(plan, w.unlockSkinStorageItem(usedTemplate)...)
	}
	plan = append(plan,
		outboundPacket{"item_action_ack", 1, protocol.CmdUseStackableAction, protocol.UseStackableActionSuccess(r)},
		outboundPacket{"item_action_inventory_updated", 0, 14, update},
	)
	if didExpand || b.ExpandedSlots > 0 && (r.ActionType == 0x28) {
		slots := expandedSlots
		if slots == 0 {
			slots = b.ExpandedSlots
		}
		if slots > 0 {
			plan = append(plan, outboundPacket{"inventory_expanded", 0, 66, protocol.InventoryExpandNoti(slots)})
		}
	}
	if kind, days, ok := directContractItem(usedTemplate); ok {
		if applied {
			if err := w.loot.Store.ExtendPremiumContracts(ctx, w.account, map[byte]int64{kind: days}); err != nil {
				return nil, err
			}
		}
		if cpkts, cerr := buildContractRefreshPackets(ctx, w.loot.Store, w.fatigue, w.account, w.role.ID, map[byte]bool{kind: true}); cerr == nil {
			plan = append(plan, cpkts...)
		}
	}
	if vkind, vtarget, ok := vaultUpgradeTarget(usedTemplate); ok && w.vaults != nil {
		if vpkts, verr := applyVaultUpgrade(ctx, w.vaults, w.account, w.role, vkind, vtarget, applied); verr == nil {
			plan = append(plan, vpkts...)
		}
	}
	return plan, nil
}

// applyVaultUpgrade persists a Personal Safe I/II or Account Vault upgrade
// and builds the live client notification packet(s):
//   - Safe I:  NOTI 66 subType 1 (u16 1, u16 newSlots)
//   - Safe II: NOTI 66 subType 22 (u16 22, u16 newSlots)
//   - Account: NOTI 13 space 0x0c (u8 0x0c, u16 newSlots, u32 gold, u16 0)
func applyVaultUpgrade(
	ctx context.Context,
	vaults *inventory.VaultService,
	account int64,
	role storage.Character,
	kind byte,
	targetSlots uint16,
	applyMutation bool,
) ([]outboundPacket, error) {
	if vaults == nil {
		return nil, fmt.Errorf("vault service unavailable")
	}
	switch kind {
	case vaultUpgradeSafe1, vaultUpgradeSafe2:
		tab := byte(0)
		if kind == vaultUpgradeSafe2 {
			tab = 1
		}
		var slots uint16
		if applyMutation {
			next, err := vaults.UpgradePersonal(ctx, role, tab, targetSlots)
			if err != nil {
				return nil, err
			}
			slots = next
		} else {
			v, err := vaults.Store.LoadVault(ctx, role.AccountID, role.ID, vaults.Rules.InitialSlots, vaults.Rules.SourceSHA256)
			if err != nil {
				return nil, err
			}
			if tab == 1 {
				slots = v.Slots2
			} else {
				slots = v.Slots
			}
		}
		if slots == 0 {
			return nil, nil
		}
		if tab == 1 {
			return []outboundPacket{{"personal_vault2_upgraded", 0, 66, protocol.PersonalVault2UpgradeNoti(slots)}}, nil
		}
		return []outboundPacket{{"personal_vault_upgraded", 0, 66, protocol.PersonalVaultUpgradeNoti(slots)}}, nil
	case vaultUpgradeAccount:
		var av storage.AccountVaultState
		var err error
		if applyMutation {
			av, err = vaults.UpgradeAccount(ctx, account, targetSlots)
		} else {
			av, err = vaults.Store.LoadAccountVault(ctx, account)
		}
		if err != nil {
			return nil, err
		}
		if av.Slots == 0 {
			return nil, nil
		}
		p, err := protocol.EmptyAccountVault(av.Slots, av.Gold)
		if err != nil {
			return nil, err
		}
		return []outboundPacket{{"account_vault_upgraded", 0, 13, p}}, nil
	}
	return nil, nil
}

// expandInventory answers CMD 422 (0x1a6), sent by DFO_unpacked.exe (0x1439b1136)
// when right-clicking an inventory expansion ticket in the bag.
func (w *worldSession) expandInventory(p, rawFrame []byte) ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 || w.characters == nil {
		return nil, fmt.Errorf("expand inventory before character selection")
	}
	slot, e := protocol.DecodeExpandInventory(p)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("expand422/%d/%d/%x", w.role.ID, slot, sha256.Sum256(rawFrame))

	var usedTemplate uint32
	var remaining uint32
	saved, _, e := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, key, "us100-expand-inventory-422-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, err := inventory.ReadBag(current.State)
		if err != nil {
			return nil, nil, err
		}
		idx := -1
		for i, it := range b.Items {
			if it.Slot == slot && it.Amount > 0 {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, nil, fmt.Errorf("no expansion ticket at slot %d", slot)
		}
		usedTemplate = b.Items[idx].Template
		target, ok := inventoryExpandTarget(usedTemplate)
		if !ok {
			return nil, nil, fmt.Errorf("template %d is not an inventory expansion ticket", usedTemplate)
		}
		nextSlots, err := b.ExpandInventory(target)
		if err != nil {
			return nil, nil, err
		}
		if b.Items[idx].Amount > 1 {
			b.Items[idx].Amount--
			remaining = b.Items[idx].Amount
		} else {
			b.Items = append(b.Items[:idx], b.Items[idx+1:]...)
			remaining = 0
		}
		updated, err := inventory.SaveBag(current.State, b)
		if err != nil {
			return nil, nil, err
		}
		receipt, err := json.Marshal(map[string]any{"slot": slot, "template": usedTemplate, "remaining": remaining, "expanded_slots": nextSlots})
		return updated, receipt, err
	})
	if e != nil {
		return nil, e
	}
	saved.WireID = w.role.WireID
	w.role = saved

	b, e := inventory.ReadBag(saved.State)
	if e != nil {
		return nil, e
	}
	rows := b.Rows()
	if remaining == 0 {
		rows = append(rows, protocol.EmptySlotItem(slot))
		sort.Slice(rows, func(i, j int) bool {
			return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
		})
	}
	update, e := protocol.US224InventoryUpdate(rows)
	if e != nil {
		return nil, e
	}
	return []outboundPacket{
		{"expand_inventory_bag_updated", 0, 14, update},
		{"inventory_expanded", 0, 66, protocol.InventoryExpandNoti(b.ExpandedSlots)},
	}, nil
}
