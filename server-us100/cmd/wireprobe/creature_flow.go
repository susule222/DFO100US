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

// handleCreatureEggHatch answers CMD 102 (0x66, 0x143b29597):
// right-clicking a Creature Egg in Space 7 hatches it into its PVF [output index]
// creature template, persisting the change and sending ACK 102 + NOTI 14 Space 7.
func (w *worldSession) handleCreatureEggHatch(plaintext, rawFrame []byte) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("world session not ready for creature egg hatch")
	}
	space, slot, err := protocol.DecodeCreatureEggHatch(plaintext)
	if err != nil {
		return nil, err
	}
	if space != protocol.CreatureSpace || slot > inventory.CreatureSlotEnd {
		return nil, fmt.Errorf("invalid creature egg location space=%d slot=%d", space, slot)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("egg102/%d/%d/%x", w.role.ID, slot, sha256.Sum256(rawFrame))

	var eggTemplate uint32
	var hatchedTemplate uint32
	var hatchedEquip inventory.BagEquipment
	saved, _, err := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, key, "us100-creature-egg-hatch-102-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, berr := inventory.ReadBag(current.State)
		if berr != nil {
			return nil, nil, berr
		}
		idx := -1
		for i, c := range b.Creatures {
			if c.Slot == slot && c.Template != 0 {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, nil, fmt.Errorf("no creature egg at space 7 slot %d", slot)
		}
		eggTemplate = b.Creatures[idx].Template
		if w.loot != nil && w.loot.Boosters != nil {
			hatchedTemplate = w.loot.Boosters.CreatureEggs[eggTemplate]
		}
		if hatchedTemplate == 0 {
			return nil, nil, fmt.Errorf("creature %d at slot %d is not a hatchable egg", eggTemplate, slot)
		}
		b.Creatures[idx].Template = hatchedTemplate
		hatchedEquip = b.Creatures[idx]
		updated, serr := inventory.SaveBag(current.State, b)
		if serr != nil {
			return nil, nil, serr
		}
		receipt, merr := json.Marshal(map[string]any{
			"slot":    slot,
			"egg":     eggTemplate,
			"hatched": hatchedTemplate,
		})
		return updated, receipt, merr
	})
	if err != nil {
		return nil, err
	}
	saved.WireID = w.role.WireID
	w.role = saved

	if hatchedEquip.Template == 0 {
		// Replay path: read current hatched creature at slot from saved bag.
		if b, berr := inventory.ReadBag(saved.State); berr == nil {
			for _, c := range b.Creatures {
				if c.Slot == slot {
					hatchedEquip = c
					break
				}
			}
		}
	}
	plan := []outboundPacket{
		{"creature_egg_hatch_ack", 1, protocol.CmdCreatureEggHatch, protocol.CreatureEggHatchSuccess(space, slot)},
	}
	if hatchedEquip.Template != 0 {
		if upd, uerr := protocol.US224CreatureUpdate([][protocol.CurrentItemRecordSize]byte{inventory.EquipmentRow(hatchedEquip)}); uerr == nil {
			plan = append(plan, outboundPacket{"creature_egg_hatched_sync", 0, 14, upd})
		}
	}
	return plan, nil
}
