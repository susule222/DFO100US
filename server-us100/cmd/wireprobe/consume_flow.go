package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"encoding/binary"
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
