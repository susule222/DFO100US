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
		receipt, err := json.Marshal(map[string]any{"slot": r.Slot, "template": usedTemplate, "action": r.ActionType, "remaining": remaining})
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
	plan := []outboundPacket{
		{"item_action_ack", 1, protocol.CmdUseStackableAction, protocol.UseStackableActionSuccess(r)},
		{"item_action_inventory_updated", 0, 14, update},
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
	return plan, nil
}
