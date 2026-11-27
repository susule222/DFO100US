package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"fmt"
	"sort"
	"time"
)

type disjointSession struct {
	nonce       [16]byte
	initialized bool
}

func (s *disjointSession) handle(service *inventory.WearService, w *worldSession, p, raw []byte) ([]outboundPacket, error) {
	if service == nil || w == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("disjoint requires owned character")
	}
	req, err := protocol.DecodeDisjoint(p)
	if err != nil {
		return nil, err
	}
	if !s.initialized {
		if _, err = rand.Read(s.nonce[:]); err != nil {
			return nil, err
		}
		s.initialized = true
	}
	hash := sha256.Sum256(raw)
	key := fmt.Sprintf("disjoint:%x:%x", s.nonce, hash)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved, rewards, accountMaterials, applied, err := service.Disjoint(ctx, w.role, key, req)
	if err != nil {
		return nil, err
	}

	var plan []outboundPacket
	if applied {
		ack, err := protocol.DisjointSuccess(req, rewards)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"disjoint_ack", 1, 26, ack})
	}

	// Clear dismantled equipment/avatar slots authoritatively via NOTI 14.
	if req.Space == 0 {
		rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(req.Items))
		for _, it := range req.Items {
			rows = append(rows, protocol.OrdinaryItem(it.Slot, 0, 0))
		}
		sort.Slice(rows, func(i, j int) bool {
			return uint16(rows[i][0])|uint16(rows[i][1])<<8 < uint16(rows[j][0])|uint16(rows[j][1])<<8
		})
		body, err := protocol.US224InventorySpaceUpdate(0, rows)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"disjoint_slots_updated", 0, 14, body})
	} else if req.Space == 1 {
		rows := make([][protocol.CurrentItemRecordSize]byte, 0, len(req.Items))
		for _, it := range req.Items {
			rows = append(rows, protocol.EmptySlotItem(it.Slot))
		}
		sort.Slice(rows, func(i, j int) bool {
			return uint16(rows[i][0])|uint16(rows[i][1])<<8 < uint16(rows[j][0])|uint16(rows[j][1])<<8
		})
		body, err := protocol.US224InventorySpaceUpdate(1, rows)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"disjoint_avatar_slots_updated", 0, 14, body})
	}

	// Sync updated Cube Fragment balances to the client's Account Material /
	// Cube Warehouse (NOTI 1140, mode=1 incremental update -> UI 0x175 refresh).
	if len(rewards) > 0 {
		entries := make([]protocol.AccountMaterialEntry, 0, len(rewards))
		for _, r := range rewards {
			entries = append(entries, protocol.AccountMaterialEntry{
				Template: r.Template,
				Amount:   accountMaterials[r.Template],
			})
		}
		matBody, err := protocol.US224AccountMaterials(false, entries)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"account_materials_updated", 0, 1140, matBody})
	}

	w.role = saved
	return plan, nil
}
