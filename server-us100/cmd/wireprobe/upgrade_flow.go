package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"
)

func secureRandInt(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

func equipmentMeta(wear *inventory.WearService, tpl uint32) (minLevel, rarity int32) {
	if wear != nil && wear.Catalog != nil {
		if ml, rr, ok := wear.Catalog.Meta(tpl); ok {
			return ml, rr
		}
	}
	return 100, 4
}

func npcUpgradeSuccessRate(currentLevel byte) int {
	switch {
	case currentLevel <= 3:
		return 100
	case currentLevel == 4:
		return 80
	case currentLevel == 5:
		return 70
	case currentLevel == 6:
		return 60
	case currentLevel == 7:
		return 50
	case currentLevel == 8:
		return 40
	case currentLevel == 9:
		return 35
	case currentLevel == 10:
		return 30
	case currentLevel == 11:
		return 25
	default:
		return 20
	}
}

func consumeBagItemOne(items []inventory.BagItem, slot uint16, expectedTpl uint32) ([]inventory.BagItem, uint32, uint32, error) {
	idx := -1
	for i, it := range items {
		if it.Slot == slot && it.Amount > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return items, 0, 0, fmt.Errorf("no stackable at slot %d", slot)
	}
	if expectedTpl != 0 && items[idx].Template != expectedTpl {
		return items, 0, 0, fmt.Errorf("slot %d template %d does not match %d", slot, items[idx].Template, expectedTpl)
	}
	tpl := items[idx].Template
	out := append([]inventory.BagItem(nil), items...)
	var remaining uint32
	if out[idx].Amount > 1 {
		out[idx].Amount--
		remaining = out[idx].Amount
	} else {
		out = append(out[:idx], out[idx+1:]...)
		remaining = 0
	}
	return out, tpl, remaining, nil
}

func findEquipmentIndex(list []inventory.BagEquipment, slot uint16, tpl uint32) int {
	for i, eq := range list {
		if eq.Slot == slot && (tpl == 0 || eq.Template == tpl) {
			return i
		}
	}
	return -1
}

// handleUpgradeItem answers US v2.24 CMD 80 (ENUM_CMDPACKET_UPGRADE_ITEM) for:
//   - Reinforcement Tickets ([equipment reinforcement ticket], mode=0)
//   - Amplification Tickets ([equipment amplify reinforcement ticket], mode=1)
//   - Kiri Reinforce / Klonter Amplify NPC upgrades (ticket_slot=0xffff)
func handleUpgradeItem(
	ctx context.Context,
	store *storage.Store,
	wear *inventory.WearService,
	tickets *catalog.TicketCatalog,
	account int64,
	role storage.Character,
	rawFrame, plaintext []byte,
	rng func(int) int,
) (storage.Character, []outboundPacket, protocol.UpgradeItemResult, error) {
	var res protocol.UpgradeItemResult
	if store == nil || role.ID == 0 {
		return role, nil, res, fmt.Errorf("upgrade requires owned character")
	}
	req, err := protocol.DecodeUpgradeItem(plaintext)
	if err != nil {
		return role, nil, res, err
	}
	if rng == nil {
		rng = secureRandInt
	}
	key := fmt.Sprintf("upgrade80/%d/%d/%d/%d/%x", role.ID, req.Mode, req.TargetSpace, req.TargetSlot, sha256.Sum256(rawFrame))

	var consumedTicketSlot uint16 = 0xffff
	var ticketRemaining uint32
	var consumedProtectSlot uint16 = 0xffff
	var protectRemoved bool
	var updatedEquip inventory.BagEquipment
	var bagAfter inventory.Bag

	saved, _, err := store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "us100-upgrade-80-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		var targetList []inventory.BagEquipment
		if req.TargetSpace == 0 {
			targetList = append([]inventory.BagEquipment(nil), b.Equipment...)
		} else {
			targetList = append([]inventory.BagEquipment(nil), b.Worn...)
		}
		eqIdx := findEquipmentIndex(targetList, req.TargetSlot, req.TargetTemplate)
		if eqIdx < 0 {
			return nil, nil, fmt.Errorf("equipment %d not found at space %d slot %d", req.TargetTemplate, req.TargetSpace, req.TargetSlot)
		}
		eq := targetList[eqIdx]
		if req.Mode == 1 && (eq.AmplifyType == 0 || eq.AmplifyType == 0x80) {
			return nil, nil, fmt.Errorf("equipment at slot %d is not purified for amplification", req.TargetSlot)
		}
		if req.Mode == 0 && eq.AmplifyType != 0 {
			return nil, nil, fmt.Errorf("equipment at slot %d is amplified and cannot use normal reinforcement", req.TargetSlot)
		}

		oldLevel := eq.EffectiveUpgrade()
		newLevel := oldLevel
		var outcome byte = 1
		var useTicketType byte = 0
		minLv, rarity := equipmentMeta(wear, eq.Template)

		if req.TicketSlot != 0xffff {
			// Ticket upgrade path (Reinforcement Ticket or Amplification Ticket)
			itemsAfter, ticketTpl, rem, ce := consumeBagItemOne(b.Items, req.TicketSlot, 0)
			if ce != nil {
				return nil, nil, ce
			}
			tdef := catalog.UpgradeTicket{
				Kind:     map[byte]string{0: "reinforce", 1: "amplify"}[req.Mode],
				Level:    10,
				Rate:     50,
				Mode:     "fixed",
				MaxLevel: -1,
			}
			if tickets != nil {
				if found, ok := tickets.Tickets[ticketTpl]; ok {
					tdef = found
				}
			}
			if req.Mode == 1 && tdef.Kind != "amplify" {
				return nil, nil, fmt.Errorf("ticket %d (kind %s) cannot be used for amplification", ticketTpl, tdef.Kind)
			}
			if req.Mode == 0 && tdef.Kind != "reinforce" {
				return nil, nil, fmt.Errorf("ticket %d (kind %s) cannot be used for reinforcement", ticketTpl, tdef.Kind)
			}
			targetLv := int(tdef.Level)
			if tdef.Mode == "increase" {
				targetLv = int(oldLevel) + int(tdef.Level)
				if tdef.MaxLevel >= 0 && targetLv > tdef.MaxLevel {
					targetLv = tdef.MaxLevel
				}
			}
			if targetLv < 1 {
				targetLv = 1
			}
			if targetLv > 15 {
				targetLv = 15
			}
			if tdef.Mode == "fixed" && int(oldLevel) >= targetLv {
				return nil, nil, fmt.Errorf("equipment +%d already at or above ticket level +%d", oldLevel, targetLv)
			}

			b.Items = itemsAfter
			consumedTicketSlot = req.TicketSlot
			ticketRemaining = rem
			useTicketType = 1

			rate := int(tdef.Rate)
			if rate < 1 {
				rate = 1
			}
			if rate > 100 {
				rate = 100
			}
			if rng(100) < rate {
				outcome = 0
				newLevel = byte(targetLv)
			} else {
				outcome = 1
				newLevel = oldLevel
			}
		} else {
			// NPC Kiri / Klonter +1 upgrade path
			useTicketType = 0
			if oldLevel >= 31 {
				return nil, nil, fmt.Errorf("equipment already at max level +%d", oldLevel)
			}
			rate := npcUpgradeSuccessRate(oldLevel)
			if rng(100) < rate {
				outcome = 0
				newLevel = oldLevel + 1
			} else if oldLevel < 10 {
				outcome = 1
				newLevel = oldLevel
			} else if req.ProtectSlot != 0xffff {
				itemsAfter, _, protRem, pe := consumeBagItemOne(b.Items, req.ProtectSlot, 0)
				if pe == nil {
					b.Items = itemsAfter
					consumedProtectSlot = req.ProtectSlot
					protectRemoved = (protRem == 0)
					outcome = 1
					newLevel = oldLevel
				} else {
					outcome = 2
					newLevel = oldLevel - 1
				}
			} else {
				outcome = 2
				newLevel = oldLevel - 1
			}
		}

		eq.Upgrade = newLevel
		if req.Mode == 1 {
			eq.AmplifyValue = tickets.ComputeAmplifyValue(minLv, rarity, newLevel)
		}
		targetList[eqIdx] = eq
		if req.TargetSpace == 0 {
			b.Equipment = targetList
		} else {
			b.Worn = targetList
		}
		updatedEquip = eq
		bagAfter = b

		res = protocol.UpgradeItemResult{
			Mode:            req.Mode,
			TicketSlot:      consumedTicketSlot,
			TicketRemaining: ticketRemaining,
			ProtectSlot:     consumedProtectSlot,
			UseTicketType:   useTicketType,
			OldLevel:        oldLevel,
			Outcome:         outcome,
			NewLevel:        newLevel,
			TargetSpace:     req.TargetSpace,
			TargetSlot:      req.TargetSlot,
			AuxSlot:         0xffff,
			AuxRemaining:    0,
		}

		updated, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{
			"mode": req.Mode, "space": req.TargetSpace, "slot": req.TargetSlot,
			"ticket_slot": consumedTicketSlot, "old": oldLevel, "new": newLevel,
			"outcome": outcome, "amp_type": eq.AmplifyType, "amp_val": eq.AmplifyValue,
		})
		return updated, receipt, e
	})
	if err != nil {
		return role, nil, res, err
	}
	saved.WireID = role.WireID

	plan := []outboundPacket{
		{"upgrade_item_ack", 1, protocol.CmdUpgradeItem, protocol.UpgradeItemSuccess(res)},
	}
	// Sync ordinary bag (space 0) when a ticket/protect item was consumed or target is in space 0.
	rows0 := bagAfter.Rows()
	if consumedTicketSlot != 0xffff && ticketRemaining == 0 {
		rows0 = append(rows0, protocol.EmptySlotItem(consumedTicketSlot))
	}
	if consumedProtectSlot != 0xffff && protectRemoved && consumedProtectSlot != consumedTicketSlot {
		rows0 = append(rows0, protocol.EmptySlotItem(consumedProtectSlot))
	}
	sort.Slice(rows0, func(i, j int) bool {
		return binary.LittleEndian.Uint16(rows0[i][:]) < binary.LittleEndian.Uint16(rows0[j][:])
	})
	if upd0, e := protocol.US224InventoryUpdate(rows0); e == nil {
		plan = append(plan, outboundPacket{"upgrade_inventory_updated", 0, 14, upd0})
	}
	// Sync worn slot (space 3) and Adventurer Fame when target is worn equipment.
	if req.TargetSpace == 3 {
		if upd3, e := protocol.US224WornUpdate([][protocol.CurrentItemRecordSize]byte{inventory.EquipmentRow(updatedEquip)}); e == nil {
			plan = append(plan, outboundPacket{"upgrade_worn_updated", 0, 14, upd3})
		}
		if wear != nil && wear.Catalog != nil && saved.WireID != 0 && saved.WireID != 0xffff {
			if fameBody, e := protocol.AdventurerFameUpdate(saved.WireID, wear.Catalog.BagAdventurerFame(bagAfter)); e == nil {
				plan = append(plan, outboundPacket{"upgrade_fame_updated", 0, protocol.AdventurerFameNotiID, fameBody})
			}
		}
	}
	return saved, plan, res, nil
}

// handlePurifyItem answers US v2.24 CMD 204 (ENUM_CMDPACKET_PURIFY_ITEM).
func handlePurifyItem(
	ctx context.Context,
	store *storage.Store,
	wear *inventory.WearService,
	tickets *catalog.TicketCatalog,
	account int64,
	role storage.Character,
	rawFrame, plaintext []byte,
	rng func(int) int,
) (storage.Character, []outboundPacket, error) {
	if store == nil || role.ID == 0 {
		return role, nil, fmt.Errorf("purify requires owned character")
	}
	req, err := protocol.DecodePurifyItem(plaintext)
	if err != nil {
		return role, nil, err
	}
	if rng == nil {
		rng = secureRandInt
	}
	key := fmt.Sprintf("purify204/%d/%d/%d/%x", role.ID, req.TargetSlot, req.MaterialSlot, sha256.Sum256(rawFrame))

	var matRem uint32
	var ampType byte
	var ampVal uint16
	var bagAfter inventory.Bag

	saved, _, err := store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "us100-purify-204-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		eqIdx := findEquipmentIndex(b.Equipment, req.TargetSlot, req.TargetTemplate)
		if eqIdx < 0 {
			return nil, nil, fmt.Errorf("equipment %d not found at slot %d", req.TargetTemplate, req.TargetSlot)
		}
		itemsAfter, _, rem, ce := consumeBagItemOne(b.Items, req.MaterialSlot, req.MaterialTemplate)
		if ce != nil {
			return nil, nil, ce
		}
		eq := b.Equipment[eqIdx]
		minLv, rarity := equipmentMeta(wear, eq.Template)
		ampType = byte(1 + rng(4))
		ampVal = tickets.ComputeAmplifyValue(minLv, rarity, eq.Upgrade)
		eq.AmplifyType = ampType
		eq.AmplifyValue = ampVal
		b.Equipment = append([]inventory.BagEquipment(nil), b.Equipment...)
		b.Equipment[eqIdx] = eq
		b.Items = itemsAfter
		matRem = rem
		bagAfter = b

		updated, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"slot": req.TargetSlot, "mat_slot": req.MaterialSlot, "amp_type": ampType, "amp_val": ampVal})
		return updated, receipt, e
	})
	if err != nil {
		return role, nil, err
	}
	saved.WireID = role.WireID

	rows := bagAfter.Rows()
	if matRem == 0 {
		rows = append(rows, protocol.EmptySlotItem(req.MaterialSlot))
		sort.Slice(rows, func(i, j int) bool {
			return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
		})
	}
	plan := []outboundPacket{
		{"purify_item_ack", 1, protocol.CmdPurifyItem, protocol.PurifyItemSuccess(req.MaterialSlot, matRem, req.TargetSlot, ampType, ampVal)},
	}
	if upd, e := protocol.US224InventoryUpdate(rows); e == nil {
		plan = append(plan, outboundPacket{"purify_inventory_updated", 0, 14, upd})
	}
	return saved, plan, nil
}

// handleInvestAmplifyOption answers US v2.24 CMD 205 (ENUM_CMDPACKET_INVEST_ITEM_AMPLIFY_OPTION),
// used by Golden/Silver Amplification Grimoires to grant Dimensional stats (+ initial amplify level).
func handleInvestAmplifyOption(
	ctx context.Context,
	store *storage.Store,
	wear *inventory.WearService,
	tickets *catalog.TicketCatalog,
	account int64,
	role storage.Character,
	rawFrame, plaintext []byte,
	rng func(int) int,
) (storage.Character, []outboundPacket, error) {
	if store == nil || role.ID == 0 {
		return role, nil, fmt.Errorf("invest amplify requires owned character")
	}
	req, err := protocol.DecodeInvestAmplifyOption(plaintext)
	if err != nil {
		return role, nil, err
	}
	if rng == nil {
		rng = secureRandInt
	}
	key := fmt.Sprintf("invest205/%d/%d/%d/%d/%x", role.ID, req.Mode, req.TargetSlot, req.MaterialSlot, sha256.Sum256(rawFrame))

	var matRem uint32
	var ampType byte
	var ampVal uint16
	var newUpgrade byte
	var bagAfter inventory.Bag

	saved, _, err := store.CommitCharacterEvent(ctx, account, role.ID, role.ConfigVersion, key, "us100-invest-205-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		eqIdx := findEquipmentIndex(b.Equipment, req.TargetSlot, req.TargetTemplate)
		if eqIdx < 0 {
			return nil, nil, fmt.Errorf("equipment %d not found at slot %d", req.TargetTemplate, req.TargetSlot)
		}
		itemsAfter, matTpl, rem, ce := consumeBagItemOne(b.Items, req.MaterialSlot, req.MaterialTemplate)
		if ce != nil {
			return nil, nil, ce
		}
		if req.ChosenStat >= 1 && req.ChosenStat <= 4 {
			ampType = req.ChosenStat
		} else {
			ampType = byte(1 + rng(4))
		}
		newUpgrade = 0
		if req.Mode == 2 {
			newUpgrade = rollGrimoireLevel(tickets, matTpl, rng)
		}
		eq := b.Equipment[eqIdx]
		minLv, rarity := equipmentMeta(wear, eq.Template)
		ampVal = tickets.ComputeAmplifyValue(minLv, rarity, newUpgrade)
		eq.AmplifyType = ampType
		eq.AmplifyValue = ampVal
		eq.Upgrade = newUpgrade
		b.Equipment = append([]inventory.BagEquipment(nil), b.Equipment...)
		b.Equipment[eqIdx] = eq
		b.Items = itemsAfter
		matRem = rem
		bagAfter = b

		updated, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{
			"mode": req.Mode, "slot": req.TargetSlot, "mat_slot": req.MaterialSlot,
			"amp_type": ampType, "amp_val": ampVal, "upgrade": newUpgrade,
		})
		return updated, receipt, e
	})
	if err != nil {
		return role, nil, err
	}
	saved.WireID = role.WireID

	rows := bagAfter.Rows()
	if matRem == 0 {
		rows = append(rows, protocol.EmptySlotItem(req.MaterialSlot))
		sort.Slice(rows, func(i, j int) bool {
			return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
		})
	}
	plan := []outboundPacket{
		{"invest_amplify_ack", 1, protocol.CmdInvestAmplifyOption, protocol.InvestAmplifyOptionSuccess(req.Mode, req.MaterialSlot, matRem, req.TargetSlot, ampType, ampVal, newUpgrade)},
	}
	if upd, e := protocol.US224InventoryUpdate(rows); e == nil {
		plan = append(plan, outboundPacket{"invest_amplify_inventory_updated", 0, 14, upd})
	}
	return saved, plan, nil
}

func rollGrimoireLevel(tickets *catalog.TicketCatalog, tpl uint32, rng func(int) int) byte {
	if tickets != nil {
		if t, ok := tickets.Tickets[tpl]; ok && len(t.RandomValues) > 0 {
			total := 0
			for _, pair := range t.RandomValues {
				if pair[1] > 0 {
					total += pair[1]
				}
			}
			if total > 0 {
				pick := rng(total)
				acc := 0
				for _, pair := range t.RandomValues {
					if pair[1] <= 0 {
						continue
					}
					acc += pair[1]
					if pick < acc {
						lv := pair[0]
						if lv < 0 {
							lv = 0
						}
						if lv > 15 {
							lv = 15
						}
						return byte(lv)
					}
				}
			}
		}
	}
	// Default Golden Grimoire range (+3..+6).
	return byte(3 + rng(4))
}

func loadTicketCatalog(lootPath, boosterPath string) (*catalog.TicketCatalog, string) {
	cands := []string{}
	if boosterPath != "" {
		cands = append(cands, fmt.Sprintf("%s/tickets.generated.json", dirOf(boosterPath)))
	}
	if lootPath != "" {
		cands = append(cands, fmt.Sprintf("%s/tickets.generated.json", dirOf(lootPath)))
	}
	cands = append(cands, "configs/us100/tickets.generated.json")
	for _, p := range cands {
		if tc, err := catalog.LoadTickets(p); err == nil {
			return tc, p
		}
	}
	return nil, ""
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

var _ = time.Second
