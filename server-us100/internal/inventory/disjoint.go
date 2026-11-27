package inventory

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

// Verified US100 PVF cube fragment template IDs (etc/disjoint.etc and
// stackable/material/cubepiece_*.stk). In US v2.24, the client tracks these
// in the Account Material / Cube Warehouse (singleton 0x1428b0b80, NOTI 1140)
// rather than ordinary Material bag slots.
const (
	BlackCubeFragmentID uint32 = 3033
	WhiteCubeFragmentID uint32 = 3034
	RedCubeFragmentID   uint32 = 3035
	BlueCubeFragmentID  uint32 = 3036
	ClearCubeFragmentID uint32 = 3037
	GoldCubeFragmentID  uint32 = 3262
)

var disjointCubeOrder = [...]uint32{
	ClearCubeFragmentID,
	BlackCubeFragmentID,
	WhiteCubeFragmentID,
	RedCubeFragmentID,
	BlueCubeFragmentID,
	GoldCubeFragmentID,
}

var coloredCubeIDs = [...]uint32{
	BlackCubeFragmentID,
	WhiteCubeFragmentID,
	RedCubeFragmentID,
	BlueCubeFragmentID,
}

// IsCubeFragment reports whether template is a Cube Fragment stored in the
// US v2.24 Account Cube Warehouse (NOTI 1140) rather than ordinary bag slots.
func IsCubeFragment(template uint32) bool {
	switch template {
	case BlackCubeFragmentID, WhiteCubeFragmentID, RedCubeFragmentID,
		BlueCubeFragmentID, ClearCubeFragmentID, GoldCubeFragmentID:
		return true
	}
	return false
}

func equipmentDisjointStats(c *EquipmentCatalog, template uint32) (level uint32, rarity int32) {
	level = 1
	if c == nil {
		return level, 0
	}
	def, ok := c.index[template]
	if !ok {
		return level, 0
	}
	if m := def.Fields["[minimum level]"]; len(m) == 1 && m[0].Type == 0 && m[0].Value > 0 {
		level = uint32(m[0].Value)
	} else if g := def.Fields["[grade]"]; len(g) == 1 && g[0].Type == 0 && g[0].Value > 0 {
		level = uint32(g[0].Value)
	}
	if r := def.Fields["[rarity]"]; len(r) == 1 && r[0].Type == 0 && r[0].Value >= 0 {
		rarity = r[0].Value
	}
	return level, rarity
}

func addDisjointYield(yields map[uint32]uint32, c *EquipmentCatalog, space byte, item protocol.DisjointItem) {
	if space == 1 {
		yields[ClearCubeFragmentID] += 50
		yields[GoldCubeFragmentID] += 5
		return
	}
	lv, rar := equipmentDisjointStats(c, item.Template)
	if rar < 0 {
		rar = 0
	}
	clearAmt := lv/2 + 3 + uint32(rar)*3
	yields[ClearCubeFragmentID] += clearAmt

	if rar >= 1 {
		colorID := coloredCubeIDs[(item.Template+uint32(item.Slot))%uint32(len(coloredCubeIDs))]
		var colorAmt uint32
		switch {
		case rar == 1:
			colorAmt = 2 + lv/20
		case rar == 2:
			colorAmt = 5 + lv/15
		default:
			colorAmt = 10 + lv/10
		}
		yields[colorID] += colorAmt
	}
	if rar == 2 {
		yields[GoldCubeFragmentID] += 1
	} else if rar == 3 {
		yields[GoldCubeFragmentID] += 3
	} else if rar >= 4 {
		yields[GoldCubeFragmentID] += 5 + uint32(rar-4)*2
	}
}

// DisjointBag removes all requested equipment or avatar slots atomically and
// calculates the Cube Fragment yields destined for the Account Cube Warehouse.
// Cube fragments are NOT placed into ordinary Material bag slots because US
// v2.24 stores them in the Account Material Warehouse (0x1428b0b80 / NOTI 1140).
func DisjointBag(b Bag, c *EquipmentCatalog, req protocol.DisjointRequest) (Bag, []protocol.DisjointReward, map[uint32]uint32, error) {
	if (req.Space != 0 && req.Space != 1) || len(req.Items) == 0 {
		return b, nil, nil, fmt.Errorf("invalid disjoint request")
	}
	removeSlots := make(map[uint16]uint32, len(req.Items))
	for _, it := range req.Items {
		if _, dup := removeSlots[it.Slot]; dup {
			return b, nil, nil, fmt.Errorf("duplicate disjoint slot %d", it.Slot)
		}
		removeSlots[it.Slot] = it.Template
	}

	next := b
	if req.Space == 0 {
		kept := make([]BagEquipment, 0, len(b.Equipment))
		matched := 0
		for _, eq := range b.Equipment {
			wantTpl, ok := removeSlots[eq.Slot]
			if !ok {
				kept = append(kept, eq)
				continue
			}
			if eq.Template != wantTpl {
				return b, nil, nil, fmt.Errorf("stale equipment identity at slot %d", eq.Slot)
			}
			if eq.RentalUntil > 0 {
				return b, nil, nil, fmt.Errorf("rental equipment at slot %d cannot be disassembled", eq.Slot)
			}
			matched++
		}
		if matched != len(req.Items) {
			return b, nil, nil, fmt.Errorf("disjoint equipment slot missing")
		}
		next.Equipment = kept
	} else {
		kept := make([]BagEquipment, 0, len(b.Avatars))
		matched := 0
		for _, av := range b.Avatars {
			wantTpl, ok := removeSlots[av.Slot]
			if !ok {
				kept = append(kept, av)
				continue
			}
			if av.Template != wantTpl {
				return b, nil, nil, fmt.Errorf("stale avatar identity at slot %d", av.Slot)
			}
			matched++
		}
		if matched != len(req.Items) {
			return b, nil, nil, fmt.Errorf("disjoint avatar slot missing")
		}
		next.Avatars = kept
	}

	yields := map[uint32]uint32{}
	for _, it := range req.Items {
		addDisjointYield(yields, c, req.Space, it)
	}

	rewards := make([]protocol.DisjointReward, 0, len(yields))
	for _, cubeID := range disjointCubeOrder {
		if delta := yields[cubeID]; delta > 0 {
			// Slot is ignored by ACK 26 (0x1434a59bd) when 0x1428b1ed0 returns
			// true for account-warehouse cube fragments; Count drives the popup.
			rewards = append(rewards, protocol.DisjointReward{
				Slot:     0,
				Template: cubeID,
				Count:    delta,
			})
		}
	}
	return next, rewards, yields, nil
}

// Disjoint persists an equipment/avatar disassembly transaction and credits the
// resulting cube fragments into the Account Cube Warehouse atomically.
func (s *WearService) Disjoint(ctx context.Context, role storage.Character, key string, req protocol.DisjointRequest) (storage.Character, []protocol.DisjointReward, map[uint32]uint32, bool, error) {
	if s == nil || s.Store == nil {
		return role, nil, nil, false, fmt.Errorf("disjoint storage unavailable")
	}
	if s.BagRules.Source != role.ConfigVersion {
		return role, nil, nil, false, fmt.Errorf("disjoint bag rules source mismatch")
	}
	var rewards []protocol.DisjointReward
	saved, totals, applied, err := s.Store.CommitCharacterEventWithMaterials(ctx, role.AccountID, role.ID, role.ConfigVersion, key, "equipment-disjoint-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, map[uint32]uint32, error) {
		b, e := ReadBag(current.State)
		if e != nil {
			return nil, nil, nil, e
		}
		updated, got, deltas, e := DisjointBag(b, s.Catalog, req)
		if e != nil {
			return nil, nil, nil, e
		}
		state, e := SaveBag(current.State, updated)
		if e != nil {
			return nil, nil, nil, e
		}
		rewards = got
		receipt, e := json.Marshal(map[string]any{
			"space":           req.Space,
			"disassembler_id": req.DisassemblerID,
			"items":           req.Items,
			"rewards":         got,
		})
		return state, receipt, deltas, e
	})
	saved.WireID = role.WireID
	return saved, rewards, totals, applied, err
}

// MigrateBagCubes atomically moves any Cube Fragments left in a character's
// ordinary Material bag into the Account Cube Warehouse, and returns the
// updated character plus full account material totals for NOTI 1140.
func MigrateBagCubes(ctx context.Context, store *storage.Store, role storage.Character) (storage.Character, map[uint32]uint32, error) {
	if store == nil {
		return role, nil, fmt.Errorf("storage unavailable")
	}
	saved, totals, err := store.MigrateCharacterMaterials(ctx, role.AccountID, role.ID, func(current storage.Character) (json.RawMessage, map[uint32]uint32, error) {
		b, e := ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		deltas := map[uint32]uint32{}
		kept := make([]BagItem, 0, len(b.Items))
		for _, it := range b.Items {
			if IsCubeFragment(it.Template) {
				deltas[it.Template] += it.Amount
				continue
			}
			kept = append(kept, it)
		}
		if len(deltas) == 0 {
			return current.State, nil, nil
		}
		b.Items = kept
		state, e := SaveBag(current.State, b)
		return state, deltas, e
	})
	saved.WireID = role.WireID
	return saved, totals, err
}
